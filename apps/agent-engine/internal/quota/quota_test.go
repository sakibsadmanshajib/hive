package quota_test

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/agent-engine/internal/quota"
)

func TestNew_RejectsNonPositiveLimits(t *testing.T) {
	cases := []struct {
		name   string
		limits quota.Limits
	}{
		{"zero tenant", quota.Limits{TenantConcurrency: 0, UserConcurrency: 1}},
		{"negative tenant", quota.Limits{TenantConcurrency: -1, UserConcurrency: 1}},
		{"zero user", quota.Limits{TenantConcurrency: 1, UserConcurrency: 0}},
		{"negative user", quota.Limits{TenantConcurrency: 1, UserConcurrency: -1}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := quota.New(c.limits); err == nil {
				t.Fatalf("expected error for %+v, got nil", c.limits)
			}
		})
	}
}

func TestAcquire_TenantLimitEnforced(t *testing.T) {
	m, err := quota.New(quota.Limits{TenantConcurrency: 2, UserConcurrency: 10})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	tenant := uuid.New()

	var releases []func()
	for i := 0; i < 2; i++ {
		release, err := m.Acquire(tenant, uuid.New())
		if err != nil {
			t.Fatalf("Acquire %d: %v", i, err)
		}
		releases = append(releases, release)
	}

	if _, err := m.Acquire(tenant, uuid.New()); !errors.Is(err, quota.ErrTenantQuotaExceeded) {
		t.Fatalf("expected ErrTenantQuotaExceeded, got %v", err)
	}

	releases[0]()

	if _, err := m.Acquire(tenant, uuid.New()); err != nil {
		t.Fatalf("expected Acquire to succeed after release, got %v", err)
	}
}

func TestAcquire_UserLimitEnforced(t *testing.T) {
	m, err := quota.New(quota.Limits{TenantConcurrency: 10, UserConcurrency: 1})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	user := uuid.New()

	if _, err := m.Acquire(uuid.New(), user); err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	if _, err := m.Acquire(uuid.New(), user); !errors.Is(err, quota.ErrUserQuotaExceeded) {
		t.Fatalf("expected ErrUserQuotaExceeded, got %v", err)
	}
}

func TestAcquire_CrossTenantIsolation(t *testing.T) {
	// A heavy tenant saturating its own ceiling must never block or fail a
	// different tenant's Acquire call (issue #305 acceptance check: "a heavy
	// load from one tenant or user does not crash another's agent").
	m, err := quota.New(quota.Limits{TenantConcurrency: 1, UserConcurrency: 1})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	heavyTenant := uuid.New()
	quietTenant := uuid.New()

	if _, err := m.Acquire(heavyTenant, uuid.New()); err != nil {
		t.Fatalf("heavy tenant first acquire: %v", err)
	}
	if _, err := m.Acquire(heavyTenant, uuid.New()); !errors.Is(err, quota.ErrTenantQuotaExceeded) {
		t.Fatalf("expected heavy tenant to be throttled, got %v", err)
	}

	if _, err := m.Acquire(quietTenant, uuid.New()); err != nil {
		t.Fatalf("quiet tenant must not be affected by heavy tenant saturation: %v", err)
	}
}

func TestAcquire_DoubleReleaseIsSafe(t *testing.T) {
	m, err := quota.New(quota.Limits{TenantConcurrency: 1, UserConcurrency: 1})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	tenant, user := uuid.New(), uuid.New()
	release, err := m.Acquire(tenant, user)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	release()
	release() // must not double-decrement or panic

	tenantInUse, userInUse := m.InUse(tenant, user)
	if tenantInUse != 0 || userInUse != 0 {
		t.Fatalf("expected 0,0 after double release, got %d,%d", tenantInUse, userInUse)
	}
}

func TestAcquire_ConcurrentGoroutinesNeverOversubscribe(t *testing.T) {
	limit := 5
	m, err := quota.New(quota.Limits{TenantConcurrency: limit, UserConcurrency: 100})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	tenant := uuid.New()

	var succeeded int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if _, err := m.Acquire(tenant, uuid.New()); err == nil {
				atomic.AddInt64(&succeeded, 1)
			}
		}()
	}
	close(start)
	wg.Wait()

	if int(succeeded) != limit {
		t.Fatalf("expected exactly %d successful acquires under contention, got %d", limit, succeeded)
	}
}

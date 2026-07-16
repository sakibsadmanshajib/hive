// Package quota enforces per-tenant and per-user sandbox concurrency
// ceilings (issue #305/#308): a heavy load from one tenant or user must
// never starve another's agent-engine capacity.
package quota

import (
	"errors"
	"sync"

	"github.com/google/uuid"
)

// ErrTenantQuotaExceeded is returned when the tenant has no free concurrency
// slot.
var ErrTenantQuotaExceeded = errors.New("quota: tenant concurrency limit reached")

// ErrUserQuotaExceeded is returned when the user has no free concurrency
// slot.
var ErrUserQuotaExceeded = errors.New("quota: user concurrency limit reached")

// Limits configures the concurrency ceilings enforced by Manager. Both must
// be positive: a non-positive limit can never be satisfied, so New rejects
// it rather than silently accepting a quota that always denies or never
// limits anything.
type Limits struct {
	TenantConcurrency int
	UserConcurrency   int
}

// Manager enforces per-tenant and per-user sandbox concurrency ceilings.
// Each tenant and each user gets its own independent counter, so one
// tenant (or user) saturating its ceiling never blocks or delays another's
// Acquire call.
type Manager struct {
	limits Limits

	mu          sync.Mutex
	tenantCount map[uuid.UUID]int
	userCount   map[uuid.UUID]int
}

// New constructs a Manager. It returns an error if either limit is not
// positive.
func New(limits Limits) (*Manager, error) {
	if limits.TenantConcurrency <= 0 {
		return nil, errors.New("quota: TenantConcurrency must be positive")
	}
	if limits.UserConcurrency <= 0 {
		return nil, errors.New("quota: UserConcurrency must be positive")
	}
	return &Manager{
		limits:      limits,
		tenantCount: make(map[uuid.UUID]int),
		userCount:   make(map[uuid.UUID]int),
	}, nil
}

// Acquire reserves one concurrency slot for tenantID+userID. On success it
// returns a release func that must be called when the sandbox session ends;
// calling it more than once is safe. Both ceilings are checked and
// incremented under a single lock so concurrent Acquire calls can never
// oversubscribe either one.
func (m *Manager) Acquire(tenantID, userID uuid.UUID) (release func(), err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.tenantCount[tenantID] >= m.limits.TenantConcurrency {
		return nil, ErrTenantQuotaExceeded
	}
	if m.userCount[userID] >= m.limits.UserConcurrency {
		return nil, ErrUserQuotaExceeded
	}

	m.tenantCount[tenantID]++
	m.userCount[userID]++

	var once sync.Once
	return func() {
		once.Do(func() {
			m.mu.Lock()
			defer m.mu.Unlock()
			m.tenantCount[tenantID]--
			if m.tenantCount[tenantID] <= 0 {
				delete(m.tenantCount, tenantID)
			}
			m.userCount[userID]--
			if m.userCount[userID] <= 0 {
				delete(m.userCount, userID)
			}
		})
	}, nil
}

// InUse reports the current in-flight session count for tenantID and
// userID. It exists for tests and observability.
func (m *Manager) InUse(tenantID, userID uuid.UUID) (tenant, user int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.tenantCount[tenantID], m.userCount[userID]
}

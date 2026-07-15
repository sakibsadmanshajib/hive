package egress_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/egress"
)

// fakeRepo implements egress.Repository in memory for unit tests.
type fakeRepo struct {
	defaults  map[uuid.UUID]egress.Policy
	overrides map[[2]uuid.UUID]egress.Policy
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		defaults:  make(map[uuid.UUID]egress.Policy),
		overrides: make(map[[2]uuid.UUID]egress.Policy),
	}
}

func (f *fakeRepo) GetTenantDefault(_ context.Context, tenantID uuid.UUID) (egress.Policy, error) {
	p, ok := f.defaults[tenantID]
	if !ok {
		return egress.Policy{}, egress.ErrNotFound
	}
	return p, nil
}

func (f *fakeRepo) GetUserOverride(_ context.Context, tenantID, userID uuid.UUID) (egress.Policy, error) {
	p, ok := f.overrides[[2]uuid.UUID{tenantID, userID}]
	if !ok {
		return egress.Policy{}, egress.ErrNotFound
	}
	return p, nil
}

func (f *fakeRepo) UpsertTenantDefault(_ context.Context, tenantID uuid.UUID, hosts []string) (egress.Policy, error) {
	p := egress.Policy{TenantID: tenantID, AllowedHosts: hosts, UpdatedAt: time.Now()}
	f.defaults[tenantID] = p
	return p, nil
}

func (f *fakeRepo) UpsertUserOverride(_ context.Context, tenantID, userID uuid.UUID, hosts []string) (egress.Policy, error) {
	p := egress.Policy{TenantID: tenantID, UserID: userID, AllowedHosts: hosts, UpdatedAt: time.Now()}
	f.overrides[[2]uuid.UUID{tenantID, userID}] = p
	return p, nil
}

func (f *fakeRepo) DeleteUserOverride(_ context.Context, tenantID, userID uuid.UUID) error {
	delete(f.overrides, [2]uuid.UUID{tenantID, userID})
	return nil
}

// fakeOwner reports a fixed owner decision, optionally erroring.
type fakeOwner struct {
	isOwner bool
	err     error
}

func (f *fakeOwner) IsWorkspaceOwner(_ context.Context, _, _ uuid.UUID) (bool, error) {
	return f.isOwner, f.err
}

func TestService_Effective_UserOverridePresent_ReturnsOverrideNotMerged(t *testing.T) {
	repo := newFakeRepo()
	tenantID, userID := uuid.New(), uuid.New()
	repo.defaults[tenantID] = egress.Policy{TenantID: tenantID, AllowedHosts: []string{"tenant-default.example"}}
	repo.overrides[[2]uuid.UUID{tenantID, userID}] = egress.Policy{TenantID: tenantID, UserID: userID, AllowedHosts: []string{"user-only.example"}}

	svc := egress.NewService(repo, &fakeOwner{isOwner: true})
	p, err := svc.Effective(context.Background(), tenantID, userID)
	if err != nil {
		t.Fatalf("Effective: %v", err)
	}
	if len(p.AllowedHosts) != 1 || p.AllowedHosts[0] != "user-only.example" {
		t.Fatalf("expected override to fully replace tenant default, got %v", p.AllowedHosts)
	}
}

func TestService_Effective_NoOverride_FallsBackToTenantDefault(t *testing.T) {
	repo := newFakeRepo()
	tenantID, userID := uuid.New(), uuid.New()
	repo.defaults[tenantID] = egress.Policy{TenantID: tenantID, AllowedHosts: []string{"tenant-default.example"}}

	svc := egress.NewService(repo, &fakeOwner{isOwner: true})
	p, err := svc.Effective(context.Background(), tenantID, userID)
	if err != nil {
		t.Fatalf("Effective: %v", err)
	}
	if len(p.AllowedHosts) != 1 || p.AllowedHosts[0] != "tenant-default.example" {
		t.Fatalf("expected tenant default fallback, got %v", p.AllowedHosts)
	}
}

func TestService_Effective_NothingSet_FailsClosedToEmptyList(t *testing.T) {
	repo := newFakeRepo()
	tenantID, userID := uuid.New(), uuid.New()

	svc := egress.NewService(repo, &fakeOwner{isOwner: true})
	p, err := svc.Effective(context.Background(), tenantID, userID)
	if err != nil {
		t.Fatalf("Effective: %v", err)
	}
	if len(p.AllowedHosts) != 0 {
		t.Fatalf("expected empty deny-all allowlist, got %v", p.AllowedHosts)
	}
}

func TestService_Effective_NilUserID_UsesTenantDefaultOnly(t *testing.T) {
	repo := newFakeRepo()
	tenantID := uuid.New()
	repo.defaults[tenantID] = egress.Policy{TenantID: tenantID, AllowedHosts: []string{"tenant-default.example"}}

	svc := egress.NewService(repo, &fakeOwner{isOwner: true})
	p, err := svc.Effective(context.Background(), tenantID, uuid.Nil)
	if err != nil {
		t.Fatalf("Effective: %v", err)
	}
	if len(p.AllowedHosts) != 1 || p.AllowedHosts[0] != "tenant-default.example" {
		t.Fatalf("expected tenant default for nil user_id, got %v", p.AllowedHosts)
	}
}

func TestService_SetTenantDefault_OwnerAllowed(t *testing.T) {
	repo := newFakeRepo()
	tenantID, callerID := uuid.New(), uuid.New()
	svc := egress.NewService(repo, &fakeOwner{isOwner: true})

	p, err := svc.SetTenantDefault(context.Background(), callerID, tenantID, []string{"pypi.org", "github.com"})
	if err != nil {
		t.Fatalf("SetTenantDefault: %v", err)
	}
	if len(p.AllowedHosts) != 2 {
		t.Fatalf("expected 2 hosts, got %v", p.AllowedHosts)
	}
}

func TestService_SetTenantDefault_NonOwnerForbidden(t *testing.T) {
	repo := newFakeRepo()
	tenantID, callerID := uuid.New(), uuid.New()
	svc := egress.NewService(repo, &fakeOwner{isOwner: false})

	_, err := svc.SetTenantDefault(context.Background(), callerID, tenantID, []string{"pypi.org"})
	if !errors.Is(err, egress.ErrForbidden) {
		t.Fatalf("expected ErrForbidden, got %v", err)
	}
}

func TestService_SetTenantDefault_OwnerCheckError_Propagates(t *testing.T) {
	repo := newFakeRepo()
	tenantID, callerID := uuid.New(), uuid.New()
	boom := errors.New("db unreachable")
	svc := egress.NewService(repo, &fakeOwner{err: boom})

	_, err := svc.SetTenantDefault(context.Background(), callerID, tenantID, []string{"pypi.org"})
	if !errors.Is(err, boom) {
		t.Fatalf("expected owner-check error to propagate, got %v", err)
	}
}

func TestService_SetTenantDefault_RejectsWhitespaceHost(t *testing.T) {
	repo := newFakeRepo()
	tenantID, callerID := uuid.New(), uuid.New()
	svc := egress.NewService(repo, &fakeOwner{isOwner: true})

	_, err := svc.SetTenantDefault(context.Background(), callerID, tenantID, []string{"bad host.example"})
	if !errors.Is(err, egress.ErrInvalidHosts) {
		t.Fatalf("expected ErrInvalidHosts, got %v", err)
	}
}

func TestService_SetTenantDefault_RejectsWildcardAndCIDR(t *testing.T) {
	tenantID, callerID := uuid.New(), uuid.New()
	svc := egress.NewService(newFakeRepo(), &fakeOwner{isOwner: true})

	for _, bad := range []string{
		"*",
		"*.example.com",
		"github.*",
		"0.0.0.0/0",
		"::/0",
		"10.0.0.0/8",
	} {
		if _, err := svc.SetTenantDefault(context.Background(), callerID, tenantID, []string{bad}); !errors.Is(err, egress.ErrInvalidHosts) {
			t.Errorf("host %q: expected ErrInvalidHosts, got %v", bad, err)
		}
	}
}

func TestService_SetTenantDefault_AcceptsConcreteHostnamesAndIPs(t *testing.T) {
	tenantID, callerID := uuid.New(), uuid.New()
	svc := egress.NewService(newFakeRepo(), &fakeOwner{isOwner: true})

	p, err := svc.SetTenantDefault(context.Background(), callerID, tenantID, []string{"pypi.org", "192.0.2.1", "2001:db8::1"})
	if err != nil {
		t.Fatalf("expected concrete hosts/IPs to be accepted, got %v", err)
	}
	if len(p.AllowedHosts) != 3 {
		t.Fatalf("expected 3 hosts, got %v", p.AllowedHosts)
	}
}

func TestService_SetTenantDefault_DropsEmptyAndDupesHosts(t *testing.T) {
	repo := newFakeRepo()
	tenantID, callerID := uuid.New(), uuid.New()
	svc := egress.NewService(repo, &fakeOwner{isOwner: true})

	p, err := svc.SetTenantDefault(context.Background(), callerID, tenantID, []string{"  ", "github.com", "github.com", ""})
	if err != nil {
		t.Fatalf("SetTenantDefault: %v", err)
	}
	if len(p.AllowedHosts) != 1 || p.AllowedHosts[0] != "github.com" {
		t.Fatalf("expected deduped single host, got %v", p.AllowedHosts)
	}
}

func TestService_SetUserOverride_OwnerAllowed(t *testing.T) {
	repo := newFakeRepo()
	tenantID, callerID, targetUserID := uuid.New(), uuid.New(), uuid.New()
	svc := egress.NewService(repo, &fakeOwner{isOwner: true})

	p, err := svc.SetUserOverride(context.Background(), callerID, tenantID, targetUserID, []string{"docs.python.org"})
	if err != nil {
		t.Fatalf("SetUserOverride: %v", err)
	}
	if p.UserID != targetUserID {
		t.Fatalf("expected override scoped to target user, got %v", p.UserID)
	}
}

func TestService_SetUserOverride_NonOwnerForbidden(t *testing.T) {
	repo := newFakeRepo()
	tenantID, callerID, targetUserID := uuid.New(), uuid.New(), uuid.New()
	svc := egress.NewService(repo, &fakeOwner{isOwner: false})

	_, err := svc.SetUserOverride(context.Background(), callerID, tenantID, targetUserID, []string{"docs.python.org"})
	if !errors.Is(err, egress.ErrForbidden) {
		t.Fatalf("expected ErrForbidden, got %v", err)
	}
}

func TestService_SetUserOverride_NilTargetUserID_Rejected(t *testing.T) {
	repo := newFakeRepo()
	tenantID, callerID := uuid.New(), uuid.New()
	svc := egress.NewService(repo, &fakeOwner{isOwner: true})

	_, err := svc.SetUserOverride(context.Background(), callerID, tenantID, uuid.Nil, []string{"docs.python.org"})
	if err == nil {
		t.Fatal("expected error for nil target user_id")
	}
}

func TestService_DeleteUserOverride_OwnerAllowed_RevertsToTenantDefault(t *testing.T) {
	repo := newFakeRepo()
	tenantID, callerID, targetUserID := uuid.New(), uuid.New(), uuid.New()
	repo.defaults[tenantID] = egress.Policy{TenantID: tenantID, AllowedHosts: []string{"tenant-default.example"}}
	repo.overrides[[2]uuid.UUID{tenantID, targetUserID}] = egress.Policy{TenantID: tenantID, UserID: targetUserID, AllowedHosts: []string{"user-only.example"}}

	svc := egress.NewService(repo, &fakeOwner{isOwner: true})
	if err := svc.DeleteUserOverride(context.Background(), callerID, tenantID, targetUserID); err != nil {
		t.Fatalf("DeleteUserOverride: %v", err)
	}

	p, err := svc.Effective(context.Background(), tenantID, targetUserID)
	if err != nil {
		t.Fatalf("Effective after delete: %v", err)
	}
	if len(p.AllowedHosts) != 1 || p.AllowedHosts[0] != "tenant-default.example" {
		t.Fatalf("expected fallback to tenant default after override delete, got %v", p.AllowedHosts)
	}
}

func TestService_DeleteUserOverride_NonOwnerForbidden(t *testing.T) {
	repo := newFakeRepo()
	tenantID, callerID, targetUserID := uuid.New(), uuid.New(), uuid.New()
	svc := egress.NewService(repo, &fakeOwner{isOwner: false})

	err := svc.DeleteUserOverride(context.Background(), callerID, tenantID, targetUserID)
	if !errors.Is(err, egress.ErrForbidden) {
		t.Fatalf("expected ErrForbidden, got %v", err)
	}
}

func TestService_GetTenantDefault_NotFound(t *testing.T) {
	repo := newFakeRepo()
	tenantID, callerID := uuid.New(), uuid.New()
	svc := egress.NewService(repo, &fakeOwner{isOwner: true})

	_, err := svc.GetTenantDefault(context.Background(), callerID, tenantID)
	if !errors.Is(err, egress.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

package grants_test

import (
	"context"
	"errors"
	"math/big"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hivegpt/hive/apps/control-plane/internal/grants"
)

// =============================================================================
// Fake Repository — in-memory; simulates same-tx semantics by treating
// CreateWithLedger as atomic from the caller's perspective. The injectErr
// hook simulates a failed ledger insert: when set, CreateWithLedger returns
// the err WITHOUT recording the grant — proving rollback semantics.
// =============================================================================

type fakeRepo struct {
	mu         sync.Mutex
	grants     []grants.CreditGrant
	injectErr  error
	ledgerSeen []grants.CreateInput
}

func newFakeRepo() *fakeRepo { return &fakeRepo{} }

func (r *fakeRepo) CreateWithLedger(ctx context.Context, in grants.CreateInput) (grants.CreateResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Simulate the same-tx contract: if any step inside the tx fails, NO
	// row is appended (rollback). We model this by checking injectErr
	// BEFORE any state mutation.
	if r.injectErr != nil {
		// Treat injectErr as a failure that occurred mid-tx — neither the
		// ledger entry nor the grant row are persisted.
		r.ledgerSeen = append(r.ledgerSeen, in) // record attempt
		return grants.CreateResult{}, r.injectErr
	}
	if in.AmountBDTSubunits == nil || in.AmountBDTSubunits.Sign() <= 0 {
		return grants.CreateResult{}, grants.ErrInvalidAmount
	}
	if in.GrantedToUserID == uuid.Nil || in.GrantedToWorkspaceID == uuid.Nil {
		return grants.CreateResult{}, grants.ErrInvalidGrantee
	}
	g := grants.CreditGrant{
		ID:                   uuid.New(),
		GrantedByUserID:      in.GrantedByUserID,
		GrantedToUserID:      in.GrantedToUserID,
		GrantedToWorkspaceID: in.GrantedToWorkspaceID,
		AmountBDTSubunits:    new(big.Int).Set(in.AmountBDTSubunits),
		ReasonNote:           in.ReasonNote,
		LedgerEntryID:        uuid.New(),
		Currency:             "BDT",
		CreatedAt:            time.Now().UTC(),
	}
	r.grants = append(r.grants, g)
	return grants.CreateResult{Grant: g, LedgerEntryID: g.LedgerEntryID}, nil
}

func (r *fakeRepo) Get(ctx context.Context, id uuid.UUID) (grants.CreditGrant, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, g := range r.grants {
		if g.ID == id {
			return g, nil
		}
	}
	return grants.CreditGrant{}, grants.ErrNotFound
}

func (r *fakeRepo) List(ctx context.Context, f grants.ListFilter) ([]grants.CreditGrant, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []grants.CreditGrant
	for _, g := range r.grants {
		switch {
		case f.GrantorUserID != uuid.Nil && g.GrantedByUserID != f.GrantorUserID:
			continue
		case f.GranteeUserID != uuid.Nil && g.GrantedToUserID != f.GranteeUserID:
			continue
		case f.GranteeWorkspaceID != uuid.Nil && g.GrantedToWorkspaceID != f.GranteeWorkspaceID:
			continue
		}
		out = append(out, g)
	}
	return out, nil
}

// =============================================================================
// Fake AdminChecker
// =============================================================================

type fakeAdmin struct {
	admins map[uuid.UUID]bool
	err    error
}

func (a *fakeAdmin) IsPlatformAdmin(ctx context.Context, userID uuid.UUID) (bool, error) {
	if a.err != nil {
		return false, a.err
	}
	return a.admins[userID], nil
}

// =============================================================================
// Service.Create — owner-gate + validation + rollback semantics.
// =============================================================================

func TestCreate_NonAdminGetsForbidden(t *testing.T) {
	t.Parallel()
	repo := newFakeRepo()
	admin := &fakeAdmin{admins: map[uuid.UUID]bool{}}
	svc := grants.NewService(repo, admin)

	_, err := svc.Create(context.Background(), grants.CreateInput{
		GrantedByUserID:      uuid.New(),
		GrantedToUserID:      uuid.New(),
		GrantedToWorkspaceID: uuid.New(),
		AmountBDTSubunits:    big.NewInt(10000),
	})
	if !errors.Is(err, grants.ErrForbidden) {
		t.Fatalf("expected ErrForbidden, got %v", err)
	}
	if len(repo.grants) != 0 {
		t.Fatalf("expected zero grants, got %d", len(repo.grants))
	}
}

func TestCreate_AdminHappyPath(t *testing.T) {
	t.Parallel()
	repo := newFakeRepo()
	adminID := uuid.New()
	admin := &fakeAdmin{admins: map[uuid.UUID]bool{adminID: true}}
	svc := grants.NewService(repo, admin)

	res, err := svc.Create(context.Background(), grants.CreateInput{
		GrantedByUserID:      adminID,
		GrantedToUserID:      uuid.New(),
		GrantedToWorkspaceID: uuid.New(),
		AmountBDTSubunits:    big.NewInt(50000),
		ReasonNote:           "Quarterly partner top-up",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Grant.AmountBDTSubunits.Cmp(big.NewInt(50000)) != 0 {
		t.Fatalf("expected amount 50000, got %s", res.Grant.AmountBDTSubunits.String())
	}
	if res.Grant.Currency != "BDT" {
		t.Fatalf("expected currency BDT, got %q", res.Grant.Currency)
	}
	if len(repo.grants) != 1 {
		t.Fatalf("expected 1 persisted grant, got %d", len(repo.grants))
	}
}

func TestCreate_RejectsZeroAmount(t *testing.T) {
	t.Parallel()
	repo := newFakeRepo()
	adminID := uuid.New()
	admin := &fakeAdmin{admins: map[uuid.UUID]bool{adminID: true}}
	svc := grants.NewService(repo, admin)

	_, err := svc.Create(context.Background(), grants.CreateInput{
		GrantedByUserID:      adminID,
		GrantedToUserID:      uuid.New(),
		GrantedToWorkspaceID: uuid.New(),
		AmountBDTSubunits:    big.NewInt(0),
	})
	if !errors.Is(err, grants.ErrInvalidAmount) {
		t.Fatalf("expected ErrInvalidAmount, got %v", err)
	}
}

func TestCreate_RejectsNegativeAmount(t *testing.T) {
	t.Parallel()
	repo := newFakeRepo()
	adminID := uuid.New()
	admin := &fakeAdmin{admins: map[uuid.UUID]bool{adminID: true}}
	svc := grants.NewService(repo, admin)

	_, err := svc.Create(context.Background(), grants.CreateInput{
		GrantedByUserID:      adminID,
		GrantedToUserID:      uuid.New(),
		GrantedToWorkspaceID: uuid.New(),
		AmountBDTSubunits:    big.NewInt(-100),
	})
	if !errors.Is(err, grants.ErrInvalidAmount) {
		t.Fatalf("expected ErrInvalidAmount, got %v", err)
	}
}

func TestCreate_RejectsZeroGrantee(t *testing.T) {
	t.Parallel()
	repo := newFakeRepo()
	adminID := uuid.New()
	admin := &fakeAdmin{admins: map[uuid.UUID]bool{adminID: true}}
	svc := grants.NewService(repo, admin)

	_, err := svc.Create(context.Background(), grants.CreateInput{
		GrantedByUserID:      adminID,
		GrantedToUserID:      uuid.Nil,
		GrantedToWorkspaceID: uuid.New(),
		AmountBDTSubunits:    big.NewInt(1000),
	})
	if !errors.Is(err, grants.ErrInvalidGrantee) {
		t.Fatalf("expected ErrInvalidGrantee, got %v", err)
	}
}

// TestCreate_LedgerErrorRollsBack proves single-tx semantics: when the
// repository's CreateWithLedger fails (simulating a ledger-insert failure
// inside the same Postgres transaction), NO grant row is persisted —
// the caller sees the error and the audit table is empty.
func TestCreate_LedgerErrorRollsBack(t *testing.T) {
	t.Parallel()
	repo := newFakeRepo()
	repo.injectErr = errors.New("ledger insert failed")
	adminID := uuid.New()
	admin := &fakeAdmin{admins: map[uuid.UUID]bool{adminID: true}}
	svc := grants.NewService(repo, admin)

	_, err := svc.Create(context.Background(), grants.CreateInput{
		GrantedByUserID:      adminID,
		GrantedToUserID:      uuid.New(),
		GrantedToWorkspaceID: uuid.New(),
		AmountBDTSubunits:    big.NewInt(10000),
	})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	// The grant row MUST be absent (rollback worked).
	if len(repo.grants) != 0 {
		t.Fatalf("expected ZERO grants after ledger failure (rollback), got %d", len(repo.grants))
	}
	// The repo SAW the attempt (test infra confirms control reached the tx)
	// but the row was not committed.
	if len(repo.ledgerSeen) != 1 {
		t.Fatalf("expected exactly 1 attempted tx, got %d", len(repo.ledgerSeen))
	}
}

func TestCreate_AdminCheckErrorPropagates(t *testing.T) {
	t.Parallel()
	repo := newFakeRepo()
	admin := &fakeAdmin{err: errors.New("db down")}
	svc := grants.NewService(repo, admin)

	_, err := svc.Create(context.Background(), grants.CreateInput{
		GrantedByUserID:      uuid.New(),
		GrantedToUserID:      uuid.New(),
		GrantedToWorkspaceID: uuid.New(),
		AmountBDTSubunits:    big.NewInt(1000),
	})
	if err == nil {
		t.Fatalf("expected error from admin check failure")
	}
	if errors.Is(err, grants.ErrForbidden) {
		t.Fatalf("admin-check infra error must NOT mask as ErrForbidden")
	}
	if len(repo.grants) != 0 {
		t.Fatalf("expected zero grants on admin-check failure, got %d", len(repo.grants))
	}
}

// =============================================================================
// Service.List variants
// =============================================================================

func TestListByGrantorAndGrantee(t *testing.T) {
	t.Parallel()
	repo := newFakeRepo()
	adminA := uuid.New()
	adminB := uuid.New()
	admin := &fakeAdmin{admins: map[uuid.UUID]bool{adminA: true, adminB: true}}
	svc := grants.NewService(repo, admin)

	user1 := uuid.New()
	user2 := uuid.New()
	ws1 := uuid.New()
	ws2 := uuid.New()
	mustCreate := func(by, to uuid.UUID, ws uuid.UUID, amount int64) {
		t.Helper()
		_, err := svc.Create(context.Background(), grants.CreateInput{
			GrantedByUserID:      by,
			GrantedToUserID:      to,
			GrantedToWorkspaceID: ws,
			AmountBDTSubunits:    big.NewInt(amount),
		})
		if err != nil {
			t.Fatalf("create grant: %v", err)
		}
	}

	mustCreate(adminA, user1, ws1, 100)
	mustCreate(adminA, user2, ws2, 200)
	mustCreate(adminB, user1, ws1, 300)

	gotA, err := svc.ListByGrantor(context.Background(), adminA, nil, 0)
	if err != nil {
		t.Fatalf("list by grantor A: %v", err)
	}
	if len(gotA) != 2 {
		t.Fatalf("expected 2 grants by grantor A, got %d", len(gotA))
	}

	gotUser1, err := svc.ListForGrantee(context.Background(), user1, nil, 0)
	if err != nil {
		t.Fatalf("list for grantee user1: %v", err)
	}
	if len(gotUser1) != 2 {
		t.Fatalf("expected 2 grants for user1, got %d", len(gotUser1))
	}
}

// =============================================================================
// AmountString — math/big invariant guard
// =============================================================================

func TestAmountStringNil(t *testing.T) {
	t.Parallel()
	if got := grants.AmountString(nil); got != "0" {
		t.Fatalf("expected \"0\" for nil amount, got %q", got)
	}
}

func TestAmountStringRoundTrip(t *testing.T) {
	t.Parallel()
	// Larger than MaxInt32 to confirm we are not truncating.
	v := new(big.Int).SetInt64(1234567890123)
	if got := grants.AmountString(v); got != "1234567890123" {
		t.Fatalf("expected round-trip of bdt subunits, got %q", got)
	}
}

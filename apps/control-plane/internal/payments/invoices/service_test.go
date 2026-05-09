package invoices

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// =============================================================================
// In-memory test doubles. No DB; no Supabase Storage; no SMTP.
// =============================================================================

type fakeRepo struct {
	mu              sync.Mutex
	byID            map[uuid.UUID]Invoice
	byWorkspaceMonth map[string]Invoice // key = ws|YYYY-MM-01
	aggregateFn     func(ctx context.Context, ws uuid.UUID, p Period) ([]InvoiceLineItem, *big.Int, error)
	activeFn        func(ctx context.Context, p Period) ([]uuid.UUID, error)
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		byID:             map[uuid.UUID]Invoice{},
		byWorkspaceMonth: map[string]Invoice{},
	}
}

func wsMonthKey(ws uuid.UUID, periodStart time.Time) string {
	return fmt.Sprintf("%s|%s", ws.String(), periodStart.Format("2006-01-02"))
}

func (f *fakeRepo) InsertOrFetch(_ context.Context, in Invoice) (*Invoice, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := wsMonthKey(in.WorkspaceID, in.PeriodStart)
	if existing, ok := f.byWorkspaceMonth[key]; ok {
		copy := existing
		return &copy, nil
	}
	if in.ID == uuid.Nil {
		in.ID = uuid.New()
	}
	if in.GeneratedAt.IsZero() {
		in.GeneratedAt = time.Now().UTC()
	}
	f.byID[in.ID] = in
	f.byWorkspaceMonth[key] = in
	copy := in
	return &copy, nil
}

func (f *fakeRepo) GetByID(_ context.Context, id uuid.UUID) (*Invoice, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	inv, ok := f.byID[id]
	if !ok {
		return nil, ErrInvoiceNotFound
	}
	copy := inv
	return &copy, nil
}

func (f *fakeRepo) ListByWorkspace(_ context.Context, ws uuid.UUID, _ int) ([]Invoice, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Invoice
	for _, inv := range f.byID {
		if inv.WorkspaceID == ws {
			out = append(out, inv)
		}
	}
	return out, nil
}

func (f *fakeRepo) ListActiveWorkspaces(ctx context.Context, p Period) ([]uuid.UUID, error) {
	if f.activeFn != nil {
		return f.activeFn(ctx, p)
	}
	return nil, nil
}

func (f *fakeRepo) AggregateByModel(ctx context.Context, ws uuid.UUID, p Period) ([]InvoiceLineItem, *big.Int, error) {
	if f.aggregateFn != nil {
		return f.aggregateFn(ctx, ws, p)
	}
	return nil, big.NewInt(0), nil
}

// ---------- access checker ----------

type fakeAccess struct {
	allowed map[string]bool
	err     error
}

func (a *fakeAccess) IsWorkspaceMember(_ context.Context, userID, ws uuid.UUID) (bool, error) {
	if a.err != nil {
		return false, a.err
	}
	key := userID.String() + "|" + ws.String()
	return a.allowed[key], nil
}

// ---------- naming ----------

type fakeNamer struct{ name string }

func (n *fakeNamer) WorkspaceName(_ context.Context, _ uuid.UUID) (string, error) {
	return n.name, nil
}

// ---------- storage stub ----------

type fakeStorage struct {
	mu       sync.Mutex
	uploads  map[string][]byte // bucket|key -> bytes
	failOnce bool
	failErr  error
}

func newFakeStorage() *fakeStorage {
	return &fakeStorage{uploads: map[string][]byte{}}
}

func (s *fakeStorage) Upload(_ context.Context, bucket, key string, body bytesReader, _ int64, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failOnce {
		s.failOnce = false
		return s.failErr
	}
	buf := make([]byte, 0, 1024)
	tmp := make([]byte, 1024)
	for {
		n, err := body.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
		}
		if err != nil {
			break
		}
	}
	s.uploads[bucket+"|"+key] = buf
	return nil
}

func (s *fakeStorage) PresignedURL(_ context.Context, bucket, key string, _ time.Duration) (string, error) {
	return "https://storage.example.test/" + bucket + "/" + key + "?sig=stub", nil
}

// ---------- pdf stub ----------

type stubPDF struct {
	bytes []byte
	err   error
}

func (s *stubPDF) Render(_ Invoice, _ string) ([]byte, error) {
	if s.err != nil {
		return nil, s.err
	}
	if s.bytes == nil {
		return []byte("%PDF-1.4 stub BDT"), nil
	}
	return s.bytes, nil
}

// =============================================================================
// Tests
// =============================================================================

func TestGenerateInvoiceForPeriod_AggregatesAndPersists(t *testing.T) {
	t.Parallel()

	repo := newFakeRepo()
	ws := uuid.New()
	repo.aggregateFn = func(_ context.Context, _ uuid.UUID, _ Period) ([]InvoiceLineItem, *big.Int, error) {
		items := []InvoiceLineItem{
			{ModelID: "gpt-4o-mini", RequestCount: 100, BDTSubunits: big.NewInt(50_00)},
			{ModelID: "claude-haiku", RequestCount: 50, BDTSubunits: big.NewInt(75_00)},
		}
		return items, big.NewInt(125_00), nil
	}

	storage := newFakeStorage()
	svc := NewService(repo, storage, &stubPDF{}, &fakeAccess{}, &fakeNamer{name: "Acme"}, nil)

	period := Period{
		Start: time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
	}
	got, err := svc.GenerateInvoiceForPeriod(context.Background(), ws, period)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if got.TotalBDTSubunits.Cmp(big.NewInt(125_00)) != 0 {
		t.Fatalf("total = %v, want 125_00", got.TotalBDTSubunits)
	}
	if len(got.LineItems) != 2 {
		t.Fatalf("line items = %d, want 2", len(got.LineItems))
	}
	if got.PDFStorageKey == "" {
		t.Fatal("PDFStorageKey empty — Upload not called")
	}
	if len(storage.uploads) != 1 {
		t.Fatalf("storage uploads = %d, want 1", len(storage.uploads))
	}
}

func TestGenerateInvoiceForPeriod_IsIdempotent(t *testing.T) {
	t.Parallel()

	repo := newFakeRepo()
	ws := uuid.New()
	repo.aggregateFn = func(_ context.Context, _ uuid.UUID, _ Period) ([]InvoiceLineItem, *big.Int, error) {
		return []InvoiceLineItem{{ModelID: "m1", RequestCount: 1, BDTSubunits: big.NewInt(10_00)}}, big.NewInt(10_00), nil
	}

	storage := newFakeStorage()
	svc := NewService(repo, storage, &stubPDF{}, &fakeAccess{}, &fakeNamer{}, nil)

	period := Period{
		Start: time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
	}
	first, err := svc.GenerateInvoiceForPeriod(context.Background(), ws, period)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := svc.GenerateInvoiceForPeriod(context.Background(), ws, period)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("idempotency broken: %s vs %s", first.ID, second.ID)
	}
}

func TestGet_CrossWorkspaceReturns404Sentinel(t *testing.T) {
	t.Parallel()

	repo := newFakeRepo()
	ws := uuid.New()
	other := uuid.New()
	user := uuid.New()
	access := &fakeAccess{allowed: map[string]bool{user.String() + "|" + ws.String(): true}}
	storage := newFakeStorage()
	svc := NewService(repo, storage, &stubPDF{}, access, &fakeNamer{}, nil)

	period := Period{Start: time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC), End: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)}
	repo.aggregateFn = func(_ context.Context, _ uuid.UUID, _ Period) ([]InvoiceLineItem, *big.Int, error) {
		return nil, big.NewInt(0), nil
	}
	inv, err := svc.GenerateInvoiceForPeriod(context.Background(), other, period)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	_, err = svc.Get(context.Background(), user, inv.ID)
	if !errors.Is(err, ErrInvoiceNotFound) {
		t.Fatalf("expected ErrInvoiceNotFound, got %v", err)
	}
}

func TestPreviousMonth_HandlesYearBoundary(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 1, 2, 0, 0, 0, time.UTC)
	p := PreviousMonth(now)
	if !p.Start.Equal(time.Date(2025, 12, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("start = %v", p.Start)
	}
	if !p.End.Equal(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("end = %v", p.End)
	}
}

func TestStorageKeyFor_Deterministic(t *testing.T) {
	t.Parallel()

	ws := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	periodStart := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	got := storageKeyFor(ws, periodStart)
	want := "invoices/11111111-1111-1111-1111-111111111111/2026-04.pdf"
	if got != want {
		t.Fatalf("storage key = %q, want %q", got, want)
	}
}

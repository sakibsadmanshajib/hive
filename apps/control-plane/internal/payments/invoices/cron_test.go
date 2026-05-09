package invoices

import (
	"context"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/google/uuid"
)

// stubFailingPDF errs once per workspace whose id matches the trigger. Used
// to simulate per-workspace error isolation: one failure must not halt the
// pass.
type isolatingPDF struct {
	failFor uuid.UUID
}

func (p *isolatingPDF) Render(inv Invoice, _ string) ([]byte, error) {
	if inv.WorkspaceID == p.failFor {
		return nil, errors.New("simulated render fail")
	}
	return []byte("%PDF-1.4 stub BDT"), nil
}

func TestGenerateMonthlyInvoices_IsolatesPerWorkspaceErrors(t *testing.T) {
	t.Parallel()

	wsOk1 := uuid.New()
	wsBoom := uuid.New()
	wsOk2 := uuid.New()

	repo := newFakeRepo()
	repo.activeFn = func(_ context.Context, _ Period) ([]uuid.UUID, error) {
		return []uuid.UUID{wsOk1, wsBoom, wsOk2}, nil
	}
	repo.aggregateFn = func(_ context.Context, _ uuid.UUID, _ Period) ([]InvoiceLineItem, *big.Int, error) {
		return []InvoiceLineItem{{ModelID: "m", RequestCount: 1, BDTSubunits: big.NewInt(10_00)}}, big.NewInt(10_00), nil
	}

	storage := newFakeStorage()
	pdf := &isolatingPDF{failFor: wsBoom}
	svc := NewService(repo, storage, pdf, &fakeAccess{}, &fakeNamer{}, nil)

	cron := NewCron(svc, repo, CronConfig{})

	now := time.Date(2026, 5, 1, 2, 0, 0, 0, time.UTC)
	got, err := cron.GenerateMonthlyInvoices(context.Background(), now)
	if err != nil {
		t.Fatalf("cron pass: %v", err)
	}
	if got != 2 {
		t.Fatalf("generated = %d, want 2 (one workspace failed in isolation)", got)
	}
}

func TestGenerateMonthlyInvoices_IsIdempotent(t *testing.T) {
	t.Parallel()

	ws := uuid.New()
	repo := newFakeRepo()
	repo.activeFn = func(_ context.Context, _ Period) ([]uuid.UUID, error) {
		return []uuid.UUID{ws}, nil
	}
	repo.aggregateFn = func(_ context.Context, _ uuid.UUID, _ Period) ([]InvoiceLineItem, *big.Int, error) {
		return []InvoiceLineItem{{ModelID: "m", RequestCount: 1, BDTSubunits: big.NewInt(10_00)}}, big.NewInt(10_00), nil
	}

	storage := newFakeStorage()
	svc := NewService(repo, storage, &stubPDF{}, &fakeAccess{}, &fakeNamer{}, nil)
	cron := NewCron(svc, repo, CronConfig{})

	now := time.Date(2026, 5, 1, 2, 0, 0, 0, time.UTC)
	first, err := cron.GenerateMonthlyInvoices(context.Background(), now)
	if err != nil {
		t.Fatalf("first pass: %v", err)
	}
	second, err := cron.GenerateMonthlyInvoices(context.Background(), now)
	if err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if first != 1 || second != 1 {
		t.Fatalf("first=%d second=%d, want 1 + 1 (idempotent on same period)", first, second)
	}
	// Repository should hold exactly one row.
	repo.mu.Lock()
	rows := len(repo.byID)
	repo.mu.Unlock()
	if rows != 1 {
		t.Fatalf("repo rows = %d, want 1 (UNIQUE prevents duplicate)", rows)
	}
}

func TestPreviousMonth_GeneratesPriorWindow(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 1, 2, 0, 0, 0, time.UTC)
	p := PreviousMonth(now)
	if !p.Start.Equal(time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("start = %v", p.Start)
	}
	if !p.End.Equal(time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("end = %v", p.End)
	}
}

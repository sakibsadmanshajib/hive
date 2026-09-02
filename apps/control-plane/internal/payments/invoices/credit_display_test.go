package invoices

import (
	"context"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// doRequest drives the handler with an authenticated viewer and returns the
// recorded response.
func doRequest(t *testing.T, h *Handler, method, target string, userID uuid.UUID) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newRequestWithViewer(method, target, userID))
	return rec
}

// =============================================================================
// Issue #1681 — the invoice states Hive credits as its consumption unit.
//
// Credits are what the ledger stores and what the rest of the console prints;
// taka is what the customer pays. Both belong on the invoice, side by side, and
// neither may be derived from the other by a wrong factor. The fixture below is
// chosen so the two figures cannot be confused for one another: 524,653,338
// credits at 100 BDT per USD is 5,247 paisa, and the ratio between them is
// nowhere near the hundred that a paisa reading of the credit count would give.
// =============================================================================

func creditFixtureInvoice() Invoice {
	return Invoice{
		ID:               uuid.MustParse("cccccccc-cccc-cccc-cccc-cccccccccccc"),
		WorkspaceID:      uuid.MustParse("dddddddd-dddd-dddd-dddd-dddddddddddd"),
		PeriodStart:      time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		PeriodEnd:        time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		TotalBDTSubunits: big.NewInt(5247),
		TotalCredits:     big.NewInt(conflatedCredits),
		LineItems: []InvoiceLineItem{
			{
				ModelID:      "hive-fast",
				RequestCount: 412,
				Credits:      big.NewInt(conflatedCredits),
				BDTSubunits:  big.NewInt(5247),
			},
		},
		GeneratedAt:      time.Date(2026, 9, 2, 2, 0, 0, 0, time.UTC),
		USDBDTRate:       "100.000000",
		USDBDTRateSource: "env",
	}
}

// TestGenerateInvoiceForPeriod_RecordsTheCreditQuantity keeps the quantity on
// the row. Without it every surface has to invert the rate to recover a figure
// the ledger already stated exactly, and a rounded inverse presented as a
// ledger reading is the same class of defect as the one being fixed.
func TestGenerateInvoiceForPeriod_RecordsTheCreditQuantity(t *testing.T) {
	t.Parallel()

	repo := newFakeRepo()
	ws := uuid.New()
	repo.aggregateFn = func(_ context.Context, _ uuid.UUID, _ Period) ([]ModelCredits, error) {
		return []ModelCredits{
			{ModelID: "hive-fast", RequestCount: 412, Credits: big.NewInt(conflatedCredits)},
			{ModelID: "hive-smart", RequestCount: 8, Credits: big.NewInt(1_000_000_000)},
		}, nil
	}

	svc := NewService(repo, newFakeStorage(), &stubPDF{}, &fakeAccess{}, &fakeNamer{}, nil)
	period := Period{
		Start: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
	}
	got, err := svc.GenerateInvoiceForPeriod(context.Background(), ws, period)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	wantCredits := big.NewInt(conflatedCredits + 1_000_000_000)
	if got.TotalCredits == nil || got.TotalCredits.Cmp(wantCredits) != 0 {
		t.Fatalf("total credits = %v, want %s", got.TotalCredits, wantCredits)
	}
	sum := new(big.Int)
	for _, item := range got.LineItems {
		if item.Credits == nil {
			t.Fatalf("line %q carries no credit quantity", item.ModelID)
		}
		sum.Add(sum, item.Credits)
	}
	if sum.Cmp(wantCredits) != 0 {
		t.Fatalf("line credits sum to %s, total says %s", sum, got.TotalCredits)
	}
	if got.USDBDTRateSource == "" {
		t.Fatal("generated row records no rate source")
	}
}

// TestRender_PrintsCreditsAndTakaSeparately is acceptance criterion 3 of issue
// #1681 in executable form: a fixture with a known credit count renders both
// figures, and fails if either is the other divided by one hundred.
func TestRender_PrintsCreditsAndTakaSeparately(t *testing.T) {
	t.Parallel()

	text, err := renderInvoiceText(creditFixtureInvoice(), "Acme Workspace")
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	if !strings.Contains(text, "524,653,338") {
		t.Fatalf("rendered text does not state the credit quantity 524,653,338:\n%s", text)
	}
	if !strings.Contains(text, "BDT 52.47") {
		t.Fatalf("rendered text does not state the charged amount BDT 52.47:\n%s", text)
	}
	// The defect: the credit count read as paisa. If that figure appears, some
	// surface divided the quantity by one hundred and called the result taka.
	if strings.Contains(text, "5,246,533.38") || strings.Contains(text, "BDT 5246533.38") {
		t.Fatalf("rendered text prints the credit count as taka:\n%s", text)
	}
	// The inverse defect: the taka amount multiplied up and called credits.
	if strings.Contains(text, "Credits") && strings.Contains(text, "524700") {
		t.Fatalf("rendered text derives credits from the taka amount:\n%s", text)
	}
	if !strings.Contains(strings.ToLower(text), "credit") {
		t.Fatalf("rendered text never names the credit unit:\n%s", text)
	}
}

// TestRender_OmitsAnUnknownCreditQuantity covers the rows generated between the
// #1648 fix and this change: their taka is correct and their credit count was
// never recorded. The PDF says so rather than printing a zero, which would
// assert a month of no consumption against a real charge.
func TestRender_OmitsAnUnknownCreditQuantity(t *testing.T) {
	t.Parallel()

	inv := creditFixtureInvoice()
	inv.TotalCredits = nil
	inv.LineItems[0].Credits = nil

	text, err := renderInvoiceText(inv, "Acme Workspace")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(text, "\n0\n") {
		t.Fatalf("unknown credit quantity rendered as a measured zero:\n%s", text)
	}
	if !strings.Contains(text, "BDT 52.47") {
		t.Fatalf("charged amount disappeared with the credit quantity:\n%s", text)
	}
}

// TestInvoiceWire_CarriesCreditsAndTaka keeps the console's two figures
// independent on the wire. The console cannot label a quantity it was never
// sent, and a single number would leave it inventing the other one.
func TestInvoiceWire_CarriesCreditsAndTaka(t *testing.T) {
	t.Parallel()

	repo := newFakeRepo()
	storage := newFakeStorage()
	user := uuid.New()
	ws := uuid.New()
	access := &fakeAccess{allowed: map[string]bool{user.String() + "|" + ws.String(): true}}
	repo.aggregateFn = func(_ context.Context, _ uuid.UUID, _ Period) ([]ModelCredits, error) {
		return []ModelCredits{{ModelID: "hive-fast", RequestCount: 412, Credits: big.NewInt(conflatedCredits)}}, nil
	}
	svc := NewService(repo, storage, &stubPDF{}, access, &fakeNamer{}, nil)
	period := Period{
		Start: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
	}
	inv, err := svc.GenerateInvoiceForPeriod(context.Background(), ws, period)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	rec := doRequest(t, NewHandler(svc), http.MethodGet, "/api/v1/invoices/"+inv.ID.String(), user)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Invoice struct {
			TotalBDTSubunits string  `json:"total_bdt_subunits"`
			TotalCredits     *string `json:"total_credits"`
			LineItems        []struct {
				BDTSubunits string  `json:"bdt_subunits"`
				Credits     *string `json:"credits"`
			} `json:"line_items"`
		} `json:"invoice"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	if payload.Invoice.TotalBDTSubunits != "5247" {
		t.Fatalf("total_bdt_subunits = %q, want %q", payload.Invoice.TotalBDTSubunits, "5247")
	}
	if payload.Invoice.TotalCredits == nil || *payload.Invoice.TotalCredits != "524653338" {
		t.Fatalf("total_credits = %v, want \"524653338\"", payload.Invoice.TotalCredits)
	}
	if len(payload.Invoice.LineItems) != 1 {
		t.Fatalf("line items = %d, want 1", len(payload.Invoice.LineItems))
	}
	if payload.Invoice.LineItems[0].Credits == nil || *payload.Invoice.LineItems[0].Credits != "524653338" {
		t.Fatalf("line credits = %v, want \"524653338\"", payload.Invoice.LineItems[0].Credits)
	}
	// The wire must not carry a USD figure to a BD customer surface; the
	// existing FX tripwire lint scans for these keys and this keeps the new
	// fields inside that rule.
	if strings.Contains(rec.Body.String(), "usd") {
		t.Fatalf("invoice wire leaked a usd key: %s", rec.Body.String())
	}
}

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
// Issue #1681 — the usage statement states Hive credits as its unit, and states
// no money at all.
//
// Credits are what the ledger stores and what the rest of the console prints.
// The taka figure stays on the row for audit, where issue #1682's repair leaves
// it correct, and never reaches a customer: a usage period is a prepaid draw
// down that raises no charge, and pricing the consumed credits at the internal
// peg would disclose the internal value of a subscription's credit grant, which
// is confidential (owner ruling, 2026-09-02).
//
// The fixture keeps both numbers so the tests can assert on the presence of one
// and the absence of the other: 524,653,338 credits, stored alongside the 5,247
// paisa they convert to at the 100 BDT per USD this package pins.
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

// TestRender_StatesCreditsAndNoMoney is issue #1681's unit ruling plus the
// owner's 2026-09-02 amendment, in one assertion.
//
// The quantity is stated in Hive credits. No money figure appears at all: a
// usage period is a prepaid draw down that raises no charge, and converting the
// quantity back into money at the internal peg would disclose the internal
// value of a subscription's credit grant, which is confidential.
func TestRender_StatesCreditsAndNoMoney(t *testing.T) {
	t.Parallel()

	text, err := renderInvoiceText(creditFixtureInvoice(), "Acme Workspace")
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	if !strings.Contains(text, "524,653,338") {
		t.Fatalf("rendered text does not state the credit quantity 524,653,338:\n%s", text)
	}
	if !strings.Contains(strings.ToLower(text), "credit") {
		t.Fatalf("rendered text never names the credit unit:\n%s", text)
	}
	// The original defect: the credit count read as paisa.
	if strings.Contains(text, "5,246,533.38") || strings.Contains(text, "5246533.38") {
		t.Fatalf("rendered text prints the credit count as taka:\n%s", text)
	}
	// The amendment: no fiat figure of any kind, including the correct one.
	for _, banned := range []string{"BDT", "Taka", "taka", "\u09f3", "52.47", "64.60"} {
		if strings.Contains(text, banned) {
			t.Fatalf("usage statement carries the money marker %q; it raises no charge and must not price credits:\n%s", banned, text)
		}
	}
	if !strings.Contains(strings.ToLower(text), "no payment is due") {
		t.Fatalf("usage statement does not say that no payment is due:\n%s", text)
	}
}

// TestRender_RefusesAFiatMarkerFromAnyFutureEdit is the guard behind the rule,
// not a restatement of it. It drives the renderer's own tripwire directly, so a
// later change that reintroduces a money column fails here rather than shipping
// a priced credit quantity to a subscriber.
func TestRender_RefusesAFiatMarkerFromAnyFutureEdit(t *testing.T) {
	t.Parallel()

	for _, sample := range []string{"Total BDT 64.60", "Charged: 64.60 Taka", "\u09f364.60", "12 paisa"} {
		if err := assertNoFiatAmount(sample); err == nil {
			t.Fatalf("assertNoFiatAmount accepted %q", sample)
		}
	}
	if err := assertNoFiatAmount("Total  524,653,338 Hive credits"); err != nil {
		t.Fatalf("assertNoFiatAmount rejected a credits-only line: %v", err)
	}
}

// TestSanitize_KeepsAFiatNamedWorkspaceRenderable stops the guard above from
// failing an account for its own name. A workspace called "Taka Labs" must
// still get a statement.
func TestSanitize_KeepsAFiatNamedWorkspaceRenderable(t *testing.T) {
	t.Parallel()

	inv := creditFixtureInvoice()
	text, err := renderInvoiceText(inv, "Taka Labs BDT Division")
	if err != nil {
		t.Fatalf("render refused a workspace named after a currency: %v", err)
	}
	if !strings.Contains(text, "524,653,338") {
		t.Fatalf("credit quantity missing after sanitising the workspace name:\n%s", text)
	}
}

// TestRender_OmitsAnUnknownCreditQuantity covers the rows generated between the
// #1648 fix and this change: they never recorded a credit count. The statement
// says so rather than printing a zero, which would assert a month of no
// consumption for a period that had some.
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
	if !strings.Contains(text, "--") {
		t.Fatalf("unknown credit quantity is not rendered as an absence:\n%s", text)
	}
	// The statement still identifies itself and still says nothing is owed,
	// so a row with no recorded quantity is not silently an empty page.
	if !strings.Contains(strings.ToLower(text), "no payment is due") {
		t.Fatalf("draw-down note disappeared with the credit quantity:\n%s", text)
	}
}

// TestInvoiceWire_CarriesCreditsAndNoMoney pins the customer wire. The console
// is sent the quantity and nothing it could price, so no client, and no
// customer reading the API directly, can recover the internal value of a
// credit grant from a usage statement.
func TestInvoiceWire_CarriesCreditsAndNoMoney(t *testing.T) {
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
			TotalCredits *string `json:"total_credits"`
			LineItems    []struct {
				Credits *string `json:"credits"`
			} `json:"line_items"`
		} `json:"invoice"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
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
	// No money on the wire at all: not the USD the FX tripwire lint already
	// scans for, and not the taka figure that is still stored on the row.
	// Concrete money markers only. A generic word like "rate" or "amount"
	// matches "generated_at" and would fail on a body that carries no money at
	// all, which is a check that cries wolf rather than one that guards.
	body := strings.ToLower(rec.Body.String())
	for _, banned := range []string{"usd", "bdt", "subunit", "paisa", "taka", "৳", "5247"} {
		if strings.Contains(body, banned) {
			t.Fatalf("invoice wire leaked %q: %s", banned, rec.Body.String())
		}
	}
}

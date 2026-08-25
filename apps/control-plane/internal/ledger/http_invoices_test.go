package ledger

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/accounts"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/auth"
)

// Invoice HTTP surface: list, fetch by id, and the error-mapping contract of
// writeLedgerError: 400 validation, 404 foreign invoice, 500 opaque infra.
// Raw repository error text must never reach the response body.
func TestInvoiceEndpoints(t *testing.T) {
	repo := newStubRepo()
	userID := uuid.New()
	accountID := uuid.New()

	repo.accountsMap[accountID] = &accounts.Account{
		ID:          accountID,
		Slug:        "invoice-ws",
		DisplayName: "Invoice WS",
		AccountType: "business",
		OwnerUserID: userID,
	}
	repo.memberships = []accounts.Membership{
		{ID: uuid.New(), AccountID: accountID, UserID: userID, Role: "owner", Status: "active"},
	}

	invoice := InvoiceRow{
		ID:              uuid.New(),
		AccountID:       accountID,
		PaymentIntentID: uuid.New(),
		InvoiceNumber:   "INV-HTTP-1",
		Status:          "issued",
		Credits:         1000,
		AmountUSD:       1000,
		AmountLocal:     0,
		LocalCurrency:   "USD",
		TaxTreatment:    "none",
		Rail:            "stripe",
		LineItems:       []map[string]any{{"kind": "topup", "credits": float64(1000)}},
		CreatedAt:       time.Now().UTC(),
	}
	repo.invoices = []InvoiceRow{invoice}

	handler := newHTTPHandler(repo)
	viewer := auth.Viewer{UserID: userID, Email: "owner@example.com", EmailVerified: true}

	do := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req = req.WithContext(viewerCtx(viewer))
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		return rr
	}

	t.Run("list returns the account's invoices", func(t *testing.T) {
		rr := do("/api/v1/accounts/current/invoices")
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
		}
		var body struct {
			Invoices []InvoiceRow `json:"invoices"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
			t.Fatalf("json: %v", err)
		}
		if len(body.Invoices) != 1 || body.Invoices[0].InvoiceNumber != invoice.InvoiceNumber {
			t.Fatalf("unexpected invoices payload: %+v", body.Invoices)
		}
	})

	t.Run("get by id round trips line items", func(t *testing.T) {
		rr := do("/api/v1/accounts/current/invoices/" + invoice.ID.String())
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
		}
		var got InvoiceRow
		if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
			t.Fatalf("json: %v", err)
		}
		if got.ID != invoice.ID || len(got.LineItems) != 1 {
			t.Fatalf("round trip mismatch: %+v", got)
		}
	})

	t.Run("foreign invoice id answers 404 not a leak", func(t *testing.T) {
		rr := do("/api/v1/accounts/current/invoices/" + uuid.New().String())
		if rr.Code != http.StatusNotFound {
			t.Fatalf("expected 404 for foreign invoice, got %d: %s", rr.Code, rr.Body.String())
		}
	})
}

// TestLedgerErrorMapping covers writeLedgerError branches directly: validation
// errors map to 400 with their message; ErrNotFound maps to 404; anything else
// maps to a fixed 500 whose body never contains the underlying error text.
func TestLedgerErrorMapping(t *testing.T) {
	rr := httptest.NewRecorder()
	writeLedgerError(rr, &ValidationError{Message: "bad input"})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("validation error mapped to %d, want 400", rr.Code)
	}

	rr = httptest.NewRecorder()
	writeLedgerError(rr, ErrNotFound)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("ErrNotFound mapped to %d, want 404", rr.Code)
	}

	rr = httptest.NewRecorder()
	writeLedgerError(rr, context.DeadlineExceeded)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("infrastructure error mapped to %d, want 500", rr.Code)
	}
	if strings.Contains(rr.Body.String(), "deadline") {
		t.Fatal("raw error text leaked into the response body")
	}
}

// TestListEntriesHTTPParams pins the ledger-list query contract: invalid
// limit and cursor values are refused with 400 before any repository call,
// a full page yields next_cursor, and a repository failure answers a fixed
// 500 whose body never carries the underlying error text.
func TestListEntriesHTTPParams(t *testing.T) {
	repo := newStubRepo()
	userID := uuid.New()
	accountID := uuid.New()

	repo.accountsMap[accountID] = &accounts.Account{
		ID:          accountID,
		Slug:        "ledger-params-ws",
		DisplayName: "Ledger Params WS",
		AccountType: "business",
		OwnerUserID: userID,
	}
	repo.memberships = []accounts.Membership{
		{ID: uuid.New(), AccountID: accountID, UserID: userID, Role: "owner", Status: "active"},
	}
	for i := 0; i < 3; i++ {
		repo.entries[accountID] = append(repo.entries[accountID], LedgerEntry{
			ID: uuid.New(), AccountID: accountID, EntryType: EntryTypeGrant,
			CreditsDelta: 10, IdempotencyKey: uuid.NewString(),
		})
	}

	handler := newHTTPHandler(repo)
	viewer := auth.Viewer{UserID: userID, Email: "owner@example.com", EmailVerified: true}

	do := func(query string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/current/credits/ledger"+query, nil)
		req = req.WithContext(viewerCtx(viewer))
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		return rr
	}

	t.Run("bad limit and cursor are refused with 400", func(t *testing.T) {
		if rr := do("?limit=zero"); rr.Code != http.StatusBadRequest {
			t.Fatalf("bad limit got %d, want 400", rr.Code)
		}
		if rr := do("?limit=-3"); rr.Code != http.StatusBadRequest {
			t.Fatalf("negative limit got %d, want 400", rr.Code)
		}
		if rr := do("?cursor=not-a-uuid"); rr.Code != http.StatusBadRequest {
			t.Fatalf("bad cursor got %d, want 400", rr.Code)
		}
	})

	t.Run("full page yields next_cursor", func(t *testing.T) {
		rr := do("?limit=2&type=grant")
		if rr.Code != http.StatusOK {
			t.Fatalf("page got %d: %s", rr.Code, rr.Body.String())
		}
		var body struct {
			NextCursor *string `json:"next_cursor"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if body.NextCursor == nil || *body.NextCursor == "" {
			t.Fatal("a full page must carry next_cursor for pagination to continue")
		}
	})

	t.Run("repository failure answers opaque 500", func(t *testing.T) {
		failer := newStubRepo()
		failer.accountsMap = repo.accountsMap
		failer.memberships = repo.memberships
		failer.listErr = errors.New("pgx: connection reset")

		failingHandler := newHTTPHandler(failer)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/current/credits/ledger", nil)
		req = req.WithContext(viewerCtx(viewer))
		rr := httptest.NewRecorder()
		failingHandler.ServeHTTP(rr, req)

		if rr.Code != http.StatusInternalServerError {
			t.Fatalf("repo failure got %d, want 500", rr.Code)
		}
		if strings.Contains(rr.Body.String(), "pgx") {
			t.Fatal("raw error text leaked into the response body")
		}
	})
}

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

	"github.com/hivegpt/hive/apps/control-plane/internal/auth"
)

// =============================================================================
// HTTP handler matrix tests.
//
// Authentication: requests inject a viewer via auth.WithViewer (matches the
// production middleware contract). Membership is gated by fakeAccess; cross-
// workspace requests must surface as 404 (id-enumeration leak guard).
// =============================================================================

func newRequestWithViewer(method, target string, userID uuid.UUID) *http.Request {
	req := httptest.NewRequest(method, target, nil)
	ctx := auth.WithViewer(req.Context(), auth.Viewer{UserID: userID})
	return req.WithContext(ctx)
}

func setupHTTP(t *testing.T) (*Handler, *fakeRepo, *fakeStorage, *fakeAccess, uuid.UUID, uuid.UUID, *Invoice) {
	t.Helper()
	repo := newFakeRepo()
	storage := newFakeStorage()
	user := uuid.New()
	ws := uuid.New()
	access := &fakeAccess{allowed: map[string]bool{user.String() + "|" + ws.String(): true}}
	repo.aggregateFn = func(_ context.Context, _ uuid.UUID, _ Period) ([]InvoiceLineItem, *big.Int, error) {
		return []InvoiceLineItem{{ModelID: "m", RequestCount: 1, BDTSubunits: big.NewInt(50_00)}}, big.NewInt(50_00), nil
	}
	svc := NewService(repo, storage, &stubPDF{}, access, &fakeNamer{}, nil)
	period := Period{Start: time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC), End: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)}
	inv, err := svc.GenerateInvoiceForPeriod(context.Background(), ws, period)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	return NewHandler(svc), repo, storage, access, user, ws, inv
}

func TestHandleList_OwnerSeesWorkspaceInvoices(t *testing.T) {
	t.Parallel()
	h, _, _, _, user, ws, _ := setupHTTP(t)

	req := newRequestWithViewer(http.MethodGet, "/api/v1/invoices?workspace_id="+ws.String(), user)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body struct {
		Items []invoiceWire `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(body.Items))
	}
	// Wire format must contain zero customer-USD keys.
	rawLower := strings.ToLower(rec.Body.String())
	for _, banned := range []string{"amount_usd", "usd_", "fx_", "exchange_rate", "price_per_credit_usd"} {
		if strings.Contains(rawLower, banned) {
			t.Fatalf("wire response leaked %q: %s", banned, rec.Body.String())
		}
	}
}

func TestHandleList_NonMemberReturns404(t *testing.T) {
	t.Parallel()
	h, _, _, _, _, ws, _ := setupHTTP(t)

	stranger := uuid.New()
	req := newRequestWithViewer(http.MethodGet, "/api/v1/invoices?workspace_id="+ws.String(), stranger)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (cross-workspace leak guard)", rec.Code)
	}
}

func TestHandleGet_NotMemberReturns404NotForbidden(t *testing.T) {
	t.Parallel()
	h, _, _, _, _, _, inv := setupHTTP(t)

	stranger := uuid.New()
	req := newRequestWithViewer(http.MethodGet, "/api/v1/invoices/"+inv.ID.String(), stranger)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (avoid id-enumeration leak)", rec.Code)
	}
}

func TestHandleGet_MemberSeesInvoice(t *testing.T) {
	t.Parallel()
	h, _, _, _, user, _, inv := setupHTTP(t)

	req := newRequestWithViewer(http.MethodGet, "/api/v1/invoices/"+inv.ID.String(), user)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestHandlePDF_RedirectsToSignedURL(t *testing.T) {
	t.Parallel()
	h, _, _, _, user, _, inv := setupHTTP(t)

	req := newRequestWithViewer(http.MethodGet, "/api/v1/invoices/"+inv.ID.String()+"/pdf", user)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if loc == "" || !strings.HasPrefix(loc, "https://storage.example.test/") {
		t.Fatalf("Location header = %q, want signed URL", loc)
	}
	disp := rec.Header().Get("Content-Disposition")
	if disp == "" {
		t.Fatal("Content-Disposition header missing")
	}
}

func TestHandle_UnauthReturns401(t *testing.T) {
	t.Parallel()
	h, _, _, _, _, ws, _ := setupHTTP(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/invoices?workspace_id="+ws.String(), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

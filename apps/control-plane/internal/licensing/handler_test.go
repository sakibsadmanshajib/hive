package licensing_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/licensing"
)

type stubSource struct {
	e   licensing.Entitlement
	err error
}

func (s stubSource) Current(context.Context) (licensing.Entitlement, error) { return s.e, s.err }

type stubRecorder struct {
	recorded []licensing.Entitlement
	err      error
}

func (r *stubRecorder) Record(_ context.Context, e licensing.Entitlement) error {
	r.recorded = append(r.recorded, e)
	return r.err
}

func TestHandler_ServesCurrentEntitlement(t *testing.T) {
	want := licensing.Entitlement{Tier: "enterprise", Seats: 25, Valid: true, ValidatedAt: time.Now().UTC()}
	rec := &stubRecorder{}
	h := licensing.NewHandler(stubSource{e: want}, rec)

	req := httptest.NewRequest(http.MethodGet, "/internal/license/entitlement", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var got licensing.Entitlement
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Tier != want.Tier || got.Seats != want.Seats {
		t.Fatalf("unexpected body: %+v", got)
	}
	if len(rec.recorded) != 1 {
		t.Fatalf("expected recorder to be called once, got %d", len(rec.recorded))
	}
}

func TestHandler_SourceErrorReturns503(t *testing.T) {
	h := licensing.NewHandler(stubSource{err: errors.New("boom")}, nil)
	req := httptest.NewRequest(http.MethodGet, "/internal/license/entitlement", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestHandler_RejectsNonGET(t *testing.T) {
	h := licensing.NewHandler(stubSource{}, nil)
	req := httptest.NewRequest(http.MethodPost, "/internal/license/entitlement", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

func TestHandler_NilRecorderDoesNotPanic(t *testing.T) {
	h := licensing.NewHandler(stubSource{e: licensing.Entitlement{Tier: "cloud"}}, nil)
	req := httptest.NewRequest(http.MethodGet, "/internal/license/entitlement", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHandler_RecorderFailureDoesNotBlockRead(t *testing.T) {
	rec := &stubRecorder{err: errors.New("db unreachable")}
	h := licensing.NewHandler(stubSource{e: licensing.Entitlement{Tier: "enterprise", Seats: 10}}, rec)
	req := httptest.NewRequest(http.MethodGet, "/internal/license/entitlement", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 even when recorder fails, got %d", w.Code)
	}
}

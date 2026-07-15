package featuregate_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/featuregate"
)

// decodeGates reads the {"gates": {...}} body a StateHandler response carries.
func decodeGates(t *testing.T, rec *httptest.ResponseRecorder) map[string]bool {
	t.Helper()
	var body struct {
		Gates map[string]bool `json:"gates"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return body.Gates
}

func TestStateHandler_Authenticated_ReturnsTenantGates(t *testing.T) {
	tid := uuid.New()
	cp := &mockCP{flags: gatesOf(featuregate.FeatureRAG, featuregate.FeatureCowork)}
	srv := httptest.NewServer(cp)
	defer srv.Close()

	g := featuregate.New(featuregate.Config{ControlPlaneURL: srv.URL, TTL: 30 * time.Second})

	rec := httptest.NewRecorder()
	featuregate.NewStateHandler(g).ServeHTTP(rec, newRequest(tid))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	gates := decodeGates(t, rec)
	if !gates[string(featuregate.FeatureRAG)] {
		t.Errorf("expected %s enabled in %v", featuregate.FeatureRAG, gates)
	}
	if !gates[string(featuregate.FeatureCowork)] {
		t.Errorf("expected %s enabled in %v", featuregate.FeatureCowork, gates)
	}
	if gates[string(featuregate.FeatureVoice)] {
		t.Errorf("expected %s disabled in %v", featuregate.FeatureVoice, gates)
	}
}

func TestStateHandler_NoUser_Returns403(t *testing.T) {
	cp := &mockCP{}
	srv := httptest.NewServer(cp)
	defer srv.Close()

	g := featuregate.New(featuregate.Config{ControlPlaneURL: srv.URL, TTL: 30 * time.Second})

	req := httptest.NewRequest(http.MethodGet, "/v1/featuregate", nil) // no auth.User in context
	rec := httptest.NewRecorder()
	featuregate.NewStateHandler(g).ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 for unauthenticated request, got %d", rec.Code)
	}
	if cp.calls.Load() != 0 {
		t.Errorf("unauthenticated request must not hit control-plane, got %d calls", cp.calls.Load())
	}
}

// On a control-plane outage the handler fails closed: 200 with an empty gate
// map so a consumer hides gated UI rather than showing a button that 403s.
func TestStateHandler_FailsClosed_OnUpstreamError(t *testing.T) {
	tid := uuid.New()
	cp := &mockCP{statusCode: http.StatusInternalServerError}
	srv := httptest.NewServer(cp)
	defer srv.Close()

	g := featuregate.New(featuregate.Config{ControlPlaneURL: srv.URL, TTL: 30 * time.Second})

	rec := httptest.NewRecorder()
	featuregate.NewStateHandler(g).ServeHTTP(rec, newRequest(tid))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 (fail-closed), got %d", rec.Code)
	}
	if gates := decodeGates(t, rec); len(gates) != 0 {
		t.Errorf("expected empty gate map on upstream error, got %v", gates)
	}
}

func TestStateHandler_NonGet_Returns405(t *testing.T) {
	g := featuregate.New(featuregate.Config{ControlPlaneURL: "http://127.0.0.1:1", TTL: 30 * time.Second})

	req := httptest.NewRequest(http.MethodPost, "/v1/featuregate", nil)
	rec := httptest.NewRecorder()
	featuregate.NewStateHandler(g).ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 for POST, got %d", rec.Code)
	}
}

package featuregate_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/featuregate"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/tenant/settings"
)

// stubResolver returns a fixed map of enabled keys, or a fixed error, from a
// single ClientVisibleEnabled call (issue #293 — the handler no longer calls
// IsEnabled per known field, so this stub proves the dynamic contract).
type stubResolver struct {
	enabled map[settings.Key]bool
	err     error
}

func (s *stubResolver) ClientVisibleEnabled(_ context.Context, _ uuid.UUID) (map[settings.Key]bool, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.enabled, nil
}

func TestHandler_ReturnsDynamicGatesMap(t *testing.T) {
	tid := uuid.New()
	h := featuregate.NewHandler(&stubResolver{enabled: map[settings.Key]bool{
		settings.EnableRAG:   true,
		settings.EnableVoice: false,
	}})

	req := httptest.NewRequest(http.MethodGet, "/internal/featuregate/"+tid.String(), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp featuregate.FlagsResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Gates["ENABLE_RAG"] {
		t.Error("Gates[ENABLE_RAG] should be true")
	}
	if resp.Gates["ENABLE_VOICE"] {
		t.Error("Gates[ENABLE_VOICE] should be false")
	}
}

// TestHandler_NewGateKey_RequiresNoCodeChange is the acceptance-check test
// (issue #293): a brand-new gate key the handler has never heard of before
// passes straight through the dynamic map with zero changes to handler.go.
// This is what "a new gate costs one edit" means in practice.
func TestHandler_NewGateKey_RequiresNoCodeChange(t *testing.T) {
	tid := uuid.New()
	newKey := settings.Key("ENABLE_TOTALLY_NEW_THING")
	h := featuregate.NewHandler(&stubResolver{enabled: map[settings.Key]bool{
		newKey: true,
	}})

	req := httptest.NewRequest(http.MethodGet, "/internal/featuregate/"+tid.String(), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	var resp featuregate.FlagsResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Gates["ENABLE_TOTALLY_NEW_THING"] {
		t.Error("a novel key returned by the resolver must surface in Gates unchanged")
	}
}

func TestHandler_InvalidTenantID_Returns400(t *testing.T) {
	h := featuregate.NewHandler(&stubResolver{})
	req := httptest.NewRequest(http.MethodGet, "/internal/featuregate/not-a-uuid", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
	// The body is a JSON object; Content-Type must say so (issue #253 P2:
	// http.Error forces text/plain, which mismatches a JSON body).
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected Content-Type application/json, got %q", ct)
	}
}

func TestHandler_MethodNotAllowed(t *testing.T) {
	h := featuregate.NewHandler(&stubResolver{})
	req := httptest.NewRequest(http.MethodPost, "/internal/featuregate/"+uuid.New().String(), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected Content-Type application/json, got %q", ct)
	}
}

func TestHandler_EmptyGates_WhenResolverReturnsNothing(t *testing.T) {
	h := featuregate.NewHandler(&stubResolver{})
	req := httptest.NewRequest(http.MethodGet, "/internal/featuregate/"+uuid.New().String(), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	var resp featuregate.FlagsResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for k, v := range resp.Gates {
		if v {
			t.Errorf("no key should be enabled when resolver returns nothing, got %s=true", k)
		}
	}
}

// TestHandler_ResolverError_Returns500 covers the error path the old
// five-boolean handler never had: AllEnabled can fail (DB down), and the
// handler must surface a provider-blind 500 rather than a false-negative 200.
func TestHandler_ResolverError_Returns500(t *testing.T) {
	h := featuregate.NewHandler(&stubResolver{err: errors.New("db unreachable")})
	req := httptest.NewRequest(http.MethodGet, "/internal/featuregate/"+uuid.New().String(), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected Content-Type application/json, got %q", ct)
	}
}

package featuregate_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/featuregate"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/tenant/settings"
)

// stubResolver returns a fixed map of enabled keys.
type stubResolver struct {
	enabled map[settings.Key]bool
}

func (s *stubResolver) IsEnabled(_ context.Context, _ uuid.UUID, key settings.Key) bool {
	return s.enabled[key]
}

func TestHandler_ReturnsFlags(t *testing.T) {
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
	if !resp.RAGEnabled {
		t.Error("RAGEnabled should be true")
	}
	if resp.VoiceEnabled {
		t.Error("VoiceEnabled should be false")
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

func TestHandler_AllFlagsDefault_WhenNoneEnabled(t *testing.T) {
	h := featuregate.NewHandler(&stubResolver{})
	req := httptest.NewRequest(http.MethodGet, "/internal/featuregate/"+uuid.New().String(), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	var resp featuregate.FlagsResponse
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if resp.RAGEnabled || resp.VoiceEnabled || resp.RelayEnabled || resp.CoworkEnabled {
		t.Error("all flags must default to false when resolver returns nothing")
	}
}

// ---- SSO feature gate (issue #237) -----------------------------------------

func TestHandler_SSOEnabled_WhenSAMLKeySet(t *testing.T) {
	tid := uuid.New()
	h := featuregate.NewHandler(&stubResolver{enabled: map[settings.Key]bool{
		settings.EnableSSOSaml: true,
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
	if !resp.SSOEnabled {
		t.Error("SSOEnabled must be true when ENABLE_SSO_SAML is set")
	}
}

func TestHandler_SSOEnabled_WhenOIDCGoogleKeySet(t *testing.T) {
	tid := uuid.New()
	h := featuregate.NewHandler(&stubResolver{enabled: map[settings.Key]bool{
		settings.EnableSSOGoogle: true,
	}})
	req := httptest.NewRequest(http.MethodGet, "/internal/featuregate/"+tid.String(), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	var resp featuregate.FlagsResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.SSOEnabled {
		t.Error("SSOEnabled must be true when ENABLE_SSO_GOOGLE is set")
	}
}

func TestHandler_SSOEnabled_WhenOIDCMicrosoftKeySet(t *testing.T) {
	tid := uuid.New()
	h := featuregate.NewHandler(&stubResolver{enabled: map[settings.Key]bool{
		settings.EnableSSOMicrosoft: true,
	}})
	req := httptest.NewRequest(http.MethodGet, "/internal/featuregate/"+tid.String(), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	var resp featuregate.FlagsResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.SSOEnabled {
		t.Error("SSOEnabled must be true when ENABLE_SSO_MICROSOFT is set")
	}
}

func TestHandler_SSODisabled_WhenNoSSOKeySet(t *testing.T) {
	h := featuregate.NewHandler(&stubResolver{enabled: map[settings.Key]bool{
		settings.EnableRAG: true, // unrelated flag
	}})
	req := httptest.NewRequest(http.MethodGet, "/internal/featuregate/"+uuid.New().String(), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	var resp featuregate.FlagsResponse
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if resp.SSOEnabled {
		t.Error("SSOEnabled must be false when no SSO key is set")
	}
}

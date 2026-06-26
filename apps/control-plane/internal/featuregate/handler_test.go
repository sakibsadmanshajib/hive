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
}

func TestHandler_MethodNotAllowed(t *testing.T) {
	h := featuregate.NewHandler(&stubResolver{})
	req := httptest.NewRequest(http.MethodPost, "/internal/featuregate/"+uuid.New().String(), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
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

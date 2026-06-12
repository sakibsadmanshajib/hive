package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	platformhttp "github.com/sakibsadmanshajib/hive/apps/control-plane/internal/platform/http"
)

// =============================================================================
// Stub repository — satisfies Repository interface without a real DB.
// =============================================================================

type stubRepo struct {
	providers   []Provider
	createErr   error
	listErr     error
	getErr      error
	updateErr   error
	deleteErr   error
}

func (s *stubRepo) Create(_ context.Context, p Provider) (Provider, error) {
	if s.createErr != nil {
		return Provider{}, s.createErr
	}
	p.ID = uuid.New()
	p.CreatedAt = time.Now().UTC()
	p.UpdatedAt = time.Now().UTC()
	s.providers = append(s.providers, p)
	return p, nil
}

func (s *stubRepo) List(_ context.Context) ([]Provider, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.providers, nil
}

func (s *stubRepo) Get(_ context.Context, id uuid.UUID) (Provider, error) {
	if s.getErr != nil {
		return Provider{}, s.getErr
	}
	for _, p := range s.providers {
		if p.ID == id {
			return p, nil
		}
	}
	return Provider{}, ErrNotFound
}

func (s *stubRepo) Update(_ context.Context, id uuid.UUID, updated Provider) (Provider, error) {
	if s.updateErr != nil {
		return Provider{}, s.updateErr
	}
	for i, p := range s.providers {
		if p.ID == id {
			updated.ID = id
			updated.CreatedAt = p.CreatedAt
			updated.UpdatedAt = time.Now().UTC()
			s.providers[i] = updated
			return updated, nil
		}
	}
	return Provider{}, ErrNotFound
}

func (s *stubRepo) Delete(_ context.Context, id uuid.UUID) error {
	if s.deleteErr != nil {
		return s.deleteErr
	}
	for i, p := range s.providers {
		if p.ID == id {
			s.providers[i].Enabled = false
			return nil
		}
	}
	return ErrNotFound
}

// =============================================================================
// Helpers
// =============================================================================

const testToken = "test-internal-secret"

func newTestHandler(repo Repository) http.Handler {
	svc := NewService(repo)
	h := NewHandler(svc)
	// Wrap with internal token middleware matching catalog/routing pattern.
	return platformhttp.RequireInternalToken(testToken, h.InternalMux())
}

func bodyJSON(t *testing.T, v any) *bytes.Buffer {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("bodyJSON: %v", err)
	}
	return bytes.NewBuffer(b)
}

func decodeProvider(t *testing.T, body *bytes.Buffer) Provider {
	t.Helper()
	var p Provider
	if err := json.NewDecoder(body).Decode(&p); err != nil {
		t.Fatalf("decodeProvider: %v, body: %s", err, body.String())
	}
	return p
}

func decodeProviders(t *testing.T, body *bytes.Buffer) []Provider {
	t.Helper()
	var ps []Provider
	if err := json.NewDecoder(body).Decode(&ps); err != nil {
		t.Fatalf("decodeProviders: %v, body: %s", err, body.String())
	}
	return ps
}

// =============================================================================
// Test cases — all 7 required by plan 20-02.
// =============================================================================

// Case 1: POST with valid body returns 201 + provider JSON.
func TestCreateProviderValidBodyReturns201(t *testing.T) {
	repo := &stubRepo{}
	handler := newTestHandler(repo)

	body := map[string]any{
		"slug":           "together",
		"display_name":   "Together AI",
		"base_url":       "https://api.together.xyz/v1",
		"api_key_env":    "TOGETHER_API_KEY",
		"litellm_prefix": "together_ai/",
		"enabled":        true,
	}

	req := httptest.NewRequest(http.MethodPost, "/internal/providers", bodyJSON(t, body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(platformhttp.InternalTokenHeader, testToken)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rr.Code, rr.Body.String())
	}

	p := decodeProvider(t, rr.Body)
	if p.Slug != "together" {
		t.Errorf("expected slug 'together', got %q", p.Slug)
	}
	if p.ID == uuid.Nil {
		t.Error("expected non-nil ID")
	}
}

// Case 2: POST with duplicate slug returns 409.
func TestCreateProviderDuplicateSlugReturns409(t *testing.T) {
	repo := &stubRepo{createErr: ErrSlugConflict}
	handler := newTestHandler(repo)

	body := map[string]any{
		"slug":         "openrouter",
		"display_name": "OpenRouter",
		"base_url":     "https://openrouter.ai/api/v1",
		"api_key_env":  "OPENROUTER_API_KEY",
	}

	req := httptest.NewRequest(http.MethodPost, "/internal/providers", bodyJSON(t, body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(platformhttp.InternalTokenHeader, testToken)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rr.Code, rr.Body.String())
	}
}

// Case 3: POST with empty slug returns 400.
func TestCreateProviderEmptySlugReturns400(t *testing.T) {
	repo := &stubRepo{}
	handler := newTestHandler(repo)

	body := map[string]any{
		"slug":         "",
		"display_name": "Missing Slug",
		"base_url":     "https://example.com",
		"api_key_env":  "KEY",
	}

	req := httptest.NewRequest(http.MethodPost, "/internal/providers", bodyJSON(t, body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(platformhttp.InternalTokenHeader, testToken)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

// Case 4: GET /internal/providers returns array including seeded rows.
func TestListProvidersReturnsSeedRows(t *testing.T) {
	openrouterID := uuid.New()
	groqID := uuid.New()

	repo := &stubRepo{
		providers: []Provider{
			{
				ID:            openrouterID,
				Slug:          "openrouter",
				DisplayName:   "OpenRouter",
				BaseURL:       "https://openrouter.ai/api/v1",
				APIKeyEnv:     "OPENROUTER_API_KEY",
				LiteLLMPrefix: "openrouter/",
				Enabled:       true,
			},
			{
				ID:            groqID,
				Slug:          "groq",
				DisplayName:   "Groq",
				BaseURL:       "https://api.groq.com/openai/v1",
				APIKeyEnv:     "GROQ_API_KEY",
				LiteLLMPrefix: "groq/",
				Enabled:       true,
			},
		},
	}
	handler := newTestHandler(repo)

	req := httptest.NewRequest(http.MethodGet, "/internal/providers", nil)
	req.Header.Set(platformhttp.InternalTokenHeader, testToken)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	providers := decodeProviders(t, rr.Body)
	if len(providers) != 2 {
		t.Fatalf("expected 2 providers, got %d", len(providers))
	}

	slugs := map[string]bool{}
	for _, p := range providers {
		slugs[p.Slug] = true
	}
	if !slugs["openrouter"] || !slugs["groq"] {
		t.Errorf("expected openrouter + groq slugs, got %v", slugs)
	}
}

// Case 5: PUT updates display_name; returns 200 with updated record.
func TestUpdateProviderDisplayNameReturns200(t *testing.T) {
	id := uuid.New()
	repo := &stubRepo{
		providers: []Provider{
			{
				ID:            id,
				Slug:          "together",
				DisplayName:   "Together AI",
				BaseURL:       "https://api.together.xyz/v1",
				APIKeyEnv:     "TOGETHER_API_KEY",
				LiteLLMPrefix: "together_ai/",
				Enabled:       true,
			},
		},
	}
	handler := newTestHandler(repo)

	updateBody := map[string]any{
		"slug":           "together",
		"display_name":   "Together AI (updated)",
		"base_url":       "https://api.together.xyz/v1",
		"api_key_env":    "TOGETHER_API_KEY",
		"litellm_prefix": "together_ai/",
		"enabled":        true,
	}

	req := httptest.NewRequest(http.MethodPut, "/internal/providers/"+id.String(), bodyJSON(t, updateBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(platformhttp.InternalTokenHeader, testToken)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	p := decodeProvider(t, rr.Body)
	if p.DisplayName != "Together AI (updated)" {
		t.Errorf("expected updated display_name, got %q", p.DisplayName)
	}
}

// Case 6: DELETE sets enabled=false; subsequent GET shows enabled: false.
func TestDeleteProviderSetsEnabledFalse(t *testing.T) {
	id := uuid.New()
	repo := &stubRepo{
		providers: []Provider{
			{
				ID:          id,
				Slug:        "together",
				DisplayName: "Together AI",
				BaseURL:     "https://api.together.xyz/v1",
				APIKeyEnv:   "TOGETHER_API_KEY",
				Enabled:     true,
			},
		},
	}
	handler := newTestHandler(repo)

	// Soft delete.
	delReq := httptest.NewRequest(http.MethodDelete, "/internal/providers/"+id.String(), nil)
	delReq.Header.Set(platformhttp.InternalTokenHeader, testToken)
	delRR := httptest.NewRecorder()
	handler.ServeHTTP(delRR, delReq)

	if delRR.Code != http.StatusOK {
		t.Fatalf("expected 200 on delete, got %d: %s", delRR.Code, delRR.Body.String())
	}

	// Subsequent GET shows enabled: false.
	getReq := httptest.NewRequest(http.MethodGet, "/internal/providers/"+id.String(), nil)
	getReq.Header.Set(platformhttp.InternalTokenHeader, testToken)
	getRR := httptest.NewRecorder()
	handler.ServeHTTP(getRR, getReq)

	if getRR.Code != http.StatusOK {
		t.Fatalf("expected 200 on get, got %d: %s", getRR.Code, getRR.Body.String())
	}

	p := decodeProvider(t, getRR.Body)
	if p.Enabled {
		t.Error("expected enabled=false after soft delete")
	}
}

// Case 7: Request without shared-secret header returns 401.
func TestMissingInternalTokenReturns401(t *testing.T) {
	repo := &stubRepo{}
	handler := newTestHandler(repo)

	req := httptest.NewRequest(http.MethodGet, "/internal/providers", nil)
	// Intentionally omit the X-Internal-Token header.
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rr.Code, rr.Body.String())
	}
}

// =============================================================================
// Charset validation tests (reviewer finding 1 — YAML injection guard).
// =============================================================================

func TestCreateProviderInvalidSlugCharsReturns400(t *testing.T) {
	cases := []struct {
		name string
		slug string
	}{
		{"uppercase", "OpenRouter"},
		{"leading hyphen", "-openrouter"},
		{"newline", "open\nrouter"},
		{"colon", "open:router"},
		{"space", "open router"},
		{"too long", "a123456789012345678901234567890123456789012345678901234567890abcde"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			repo := &stubRepo{}
			handler := newTestHandler(repo)

			body := map[string]any{
				"slug":        tc.slug,
				"display_name": "Test",
				"base_url":    "https://example.com",
				"api_key_env": "VALID_KEY",
			}

			req := httptest.NewRequest(http.MethodPost, "/internal/providers", bodyJSON(t, body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set(platformhttp.InternalTokenHeader, testToken)
			rr := httptest.NewRecorder()

			handler.ServeHTTP(rr, req)

			if rr.Code != http.StatusBadRequest {
				t.Errorf("slug %q: expected 400, got %d: %s", tc.slug, rr.Code, rr.Body.String())
			}
		})
	}
}

func TestCreateProviderInvalidAPIKeyEnvReturns400(t *testing.T) {
	cases := []struct {
		name      string
		apiKeyEnv string
	}{
		{"lowercase", "openrouter_api_key"},
		{"leading digit", "1OPENROUTER_KEY"},
		{"shell injection", "KEY=$(whoami)"},
		{"space", "OPEN ROUTER"},
		{"newline", "KEY\nINJECT"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			repo := &stubRepo{}
			handler := newTestHandler(repo)

			body := map[string]any{
				"slug":        "valid-slug",
				"display_name": "Test",
				"base_url":    "https://example.com",
				"api_key_env": tc.apiKeyEnv,
			}

			req := httptest.NewRequest(http.MethodPost, "/internal/providers", bodyJSON(t, body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set(platformhttp.InternalTokenHeader, testToken)
			rr := httptest.NewRecorder()

			handler.ServeHTTP(rr, req)

			if rr.Code != http.StatusBadRequest {
				t.Errorf("api_key_env %q: expected 400, got %d: %s", tc.apiKeyEnv, rr.Code, rr.Body.String())
			}
		})
	}
}

func TestCreateProviderInvalidLiteLLMPrefixReturns400(t *testing.T) {
	cases := []struct {
		name   string
		prefix string
	}{
		{"newline", "openrouter/\n"},
		{"semicolon", "openrouter;rm"},
		{"backtick", "openrouter`cmd`"},
		{"dollar brace", "openrouter${KEY}"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			repo := &stubRepo{}
			handler := newTestHandler(repo)

			body := map[string]any{
				"slug":           "valid-slug",
				"display_name":   "Test",
				"base_url":       "https://example.com",
				"api_key_env":    "VALID_KEY",
				"litellm_prefix": tc.prefix,
			}

			req := httptest.NewRequest(http.MethodPost, "/internal/providers", bodyJSON(t, body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set(platformhttp.InternalTokenHeader, testToken)
			rr := httptest.NewRecorder()

			handler.ServeHTTP(rr, req)

			if rr.Code != http.StatusBadRequest {
				t.Errorf("litellm_prefix %q: expected 400, got %d: %s", tc.prefix, rr.Code, rr.Body.String())
			}
		})
	}
}

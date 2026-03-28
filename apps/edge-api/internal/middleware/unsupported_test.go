package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apierrors "github.com/hivegpt/hive/apps/edge-api/internal/errors"
	"github.com/hivegpt/hive/apps/edge-api/internal/matrix"
)

const testMatrixJSON = `{
  "version": "0.1.0",
  "generated": "2026-03-28",
  "endpoints": [
    {"method": "GET", "path": "/v1/models", "status": "supported_now", "phase": 1, "notes": "Lists available models"},
    {"method": "POST", "path": "/v1/chat/completions", "status": "planned_for_launch", "phase": 6, "notes": "Chat completion"},
    {"method": "GET", "path": "/v1/assistants", "status": "explicitly_unsupported_at_launch", "phase": null, "notes": "Assistants"},
    {"method": "GET", "path": "/v1/organization/users", "status": "out_of_scope", "phase": null, "notes": "Org admin"}
  ]
}`

func loadTestMatrix(t *testing.T) *matrix.SupportMatrix {
	t.Helper()
	m, err := matrix.LoadMatrixFromBytes([]byte(testMatrixJSON))
	if err != nil {
		t.Fatalf("failed to load test matrix: %v", err)
	}
	return m
}

func TestUnsupportedEndpointMiddleware(t *testing.T) {
	m := loadTestMatrix(t)
	mw := UnsupportedEndpointMiddleware(m)

	nextCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
	})

	handler := mw(next)

	tests := []struct {
		name         string
		method       string
		path         string
		wantStatus   int
		wantPassThru bool
		wantErrType  string
		wantCode     string
	}{
		{
			name:         "supported_now passes through",
			method:       "GET",
			path:         "/v1/models",
			wantStatus:   http.StatusOK,
			wantPassThru: true,
		},
		{
			name:         "planned_for_launch returns 404",
			method:       "POST",
			path:         "/v1/chat/completions",
			wantStatus:   http.StatusNotFound,
			wantPassThru: false,
			wantErrType:  "unsupported_endpoint",
			wantCode:     "endpoint_not_available",
		},
		{
			name:         "explicitly_unsupported returns 404",
			method:       "GET",
			path:         "/v1/assistants",
			wantStatus:   http.StatusNotFound,
			wantPassThru: false,
			wantErrType:  "unsupported_endpoint",
			wantCode:     "endpoint_unsupported",
		},
		{
			name:         "out_of_scope returns 404",
			method:       "GET",
			path:         "/v1/organization/users",
			wantStatus:   http.StatusNotFound,
			wantPassThru: false,
			wantErrType:  "unsupported_endpoint",
			wantCode:     "endpoint_out_of_scope",
		},
		{
			name:         "unknown endpoint returns 404",
			method:       "GET",
			path:         "/v1/totally/unknown",
			wantStatus:   http.StatusNotFound,
			wantPassThru: false,
			wantErrType:  "invalid_request_error",
			wantCode:     "unknown_endpoint",
		},
		{
			name:         "non-v1 path passes through",
			method:       "GET",
			path:         "/health",
			wantStatus:   http.StatusOK,
			wantPassThru: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nextCalled = false
			req := httptest.NewRequest(tt.method, tt.path, nil)
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", w.Code, tt.wantStatus)
			}

			if tt.wantPassThru {
				if !nextCalled {
					t.Error("expected next handler to be called, but it was not")
				}
				return
			}

			if nextCalled {
				t.Error("expected next handler NOT to be called, but it was")
			}

			// Parse error response
			var resp apierrors.OpenAIError
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("failed to parse error response: %v", err)
			}

			if resp.Error.Type != tt.wantErrType {
				t.Errorf("error type = %q, want %q", resp.Error.Type, tt.wantErrType)
			}

			if resp.Error.Code == nil {
				t.Fatal("error code = nil, want non-nil")
			}
			if *resp.Error.Code != tt.wantCode {
				t.Errorf("error code = %q, want %q", *resp.Error.Code, tt.wantCode)
			}

			if resp.Error.Param != nil {
				t.Errorf("error param = %v, want nil", *resp.Error.Param)
			}

			// Verify error JSON shape has the correct envelope
			var raw map[string]json.RawMessage
			if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
				t.Fatalf("failed to parse raw JSON: %v", err)
			}
			if _, ok := raw["error"]; !ok {
				t.Error("response missing top-level 'error' key")
			}
		})
	}
}

func TestUnsupportedEndpointMessagesAreProviderBlind(t *testing.T) {
	m := loadTestMatrix(t)
	mw := UnsupportedEndpointMiddleware(m)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	handler := mw(next)

	forbidden := []string{"provider", "upstream", "OpenAI"}

	paths := []struct {
		method string
		path   string
	}{
		{"POST", "/v1/chat/completions"},
		{"GET", "/v1/assistants"},
		{"GET", "/v1/organization/users"},
		{"GET", "/v1/totally/unknown"},
	}

	for _, p := range paths {
		req := httptest.NewRequest(p.method, p.path, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		body := w.Body.String()
		for _, word := range forbidden {
			if strings.Contains(body, word) {
				t.Errorf("response for %s %s contains forbidden word %q: %s",
					p.method, p.path, word, body)
			}
		}
	}
}

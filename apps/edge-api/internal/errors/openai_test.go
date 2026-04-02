package errors

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func ptrStr(s string) *string {
	return &s
}

func TestWriteError(t *testing.T) {
	tests := []struct {
		name         string
		httpStatus   int
		errType      string
		message      string
		code         *string
		wantStatus   int
		wantType     string
		wantMessage  string
		wantCodeNil  bool
		wantCodeVal  string
		wantParamNil bool
	}{
		{
			name:         "404 unsupported_endpoint with nil code",
			httpStatus:   http.StatusNotFound,
			errType:      "unsupported_endpoint",
			message:      "The endpoint GET /v1/unknown is not supported.",
			code:         nil,
			wantStatus:   http.StatusNotFound,
			wantType:     "unsupported_endpoint",
			wantMessage:  "The endpoint GET /v1/unknown is not supported.",
			wantCodeNil:  true,
			wantParamNil: true,
		},
		{
			name:         "400 invalid_request_error with code string",
			httpStatus:   http.StatusBadRequest,
			errType:      "invalid_request_error",
			message:      "bad param",
			code:         ptrStr("invalid_api_key"),
			wantStatus:   http.StatusBadRequest,
			wantType:     "invalid_request_error",
			wantMessage:  "bad param",
			wantCodeNil:  false,
			wantCodeVal:  "invalid_api_key",
			wantParamNil: true,
		},
		{
			name:         "500 server_error",
			httpStatus:   http.StatusInternalServerError,
			errType:      "server_error",
			message:      "internal error",
			code:         nil,
			wantStatus:   http.StatusInternalServerError,
			wantType:     "server_error",
			wantMessage:  "internal error",
			wantCodeNil:  true,
			wantParamNil: true,
		},
		{
			name:         "429 rate_limit with code",
			httpStatus:   http.StatusTooManyRequests,
			errType:      "rate_limit_error",
			message:      "rate limited",
			code:         ptrStr("rate_limit_exceeded"),
			wantStatus:   http.StatusTooManyRequests,
			wantType:     "rate_limit_error",
			wantMessage:  "rate limited",
			wantCodeNil:  false,
			wantCodeVal:  "rate_limit_exceeded",
			wantParamNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			WriteError(w, tt.httpStatus, tt.errType, tt.message, tt.code)

			// Check status code
			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", w.Code, tt.wantStatus)
			}

			// Check Content-Type
			ct := w.Header().Get("Content-Type")
			if ct != "application/json" {
				t.Errorf("Content-Type = %q, want %q", ct, "application/json")
			}

			// Parse response body
			var resp OpenAIError
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("failed to parse response body: %v", err)
			}

			if resp.Error.Message != tt.wantMessage {
				t.Errorf("message = %q, want %q", resp.Error.Message, tt.wantMessage)
			}
			if resp.Error.Type != tt.wantType {
				t.Errorf("type = %q, want %q", resp.Error.Type, tt.wantType)
			}

			// Check param is null
			if tt.wantParamNil && resp.Error.Param != nil {
				t.Errorf("param = %v, want nil", *resp.Error.Param)
			}

			// Check code
			if tt.wantCodeNil {
				if resp.Error.Code != nil {
					t.Errorf("code = %v, want nil", *resp.Error.Code)
				}
			} else {
				if resp.Error.Code == nil {
					t.Error("code = nil, want non-nil")
				} else if *resp.Error.Code != tt.wantCodeVal {
					t.Errorf("code = %q, want %q", *resp.Error.Code, tt.wantCodeVal)
				}
			}
		})
	}
}

func TestWriteErrorContentTypeBeforeBody(t *testing.T) {
	w := httptest.NewRecorder()
	WriteError(w, http.StatusNotFound, "unsupported_endpoint", "test", nil)

	ct := w.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}
}

func TestNewError(t *testing.T) {
	err := NewError("test_type", "test message", ptrStr("test_code"))
	if err.Error.Type != "test_type" {
		t.Errorf("type = %q, want %q", err.Error.Type, "test_type")
	}
	if err.Error.Message != "test message" {
		t.Errorf("message = %q, want %q", err.Error.Message, "test message")
	}
	if err.Error.Code == nil || *err.Error.Code != "test_code" {
		t.Errorf("code = %v, want %q", err.Error.Code, "test_code")
	}
	if err.Error.Param != nil {
		t.Errorf("param = %v, want nil", *err.Error.Param)
	}
}

func TestPermanentFailuresOmitRetryHeaders(t *testing.T) {
	w := httptest.NewRecorder()
	code := ptrStr("invalid_api_key")

	WriteError(w, http.StatusUnauthorized, "invalid_request_error", "Incorrect API key provided.", code)

	for _, header := range []string{
		"x-ratelimit-limit-requests",
		"x-ratelimit-remaining-requests",
		"x-ratelimit-reset-requests",
		"x-ratelimit-limit-tokens",
		"x-ratelimit-remaining-tokens",
		"x-ratelimit-reset-tokens",
		"retry-after",
	} {
		if got := w.Header().Get(header); got != "" {
			t.Fatalf("expected %s to be omitted, got %q", header, got)
		}
	}
}

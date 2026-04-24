package middleware

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestCompatHeaders(t *testing.T) {
	mw := CompatHeaders()

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/v1/models", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	t.Run("x-request-id is present and non-empty", func(t *testing.T) {
		rid := w.Header().Get("x-request-id")
		if rid == "" {
			t.Error("x-request-id is empty")
		}
		if !strings.HasPrefix(rid, "req-") {
			t.Errorf("x-request-id = %q, want prefix 'req-'", rid)
		}
	})

	t.Run("openai-version is 2020-10-01", func(t *testing.T) {
		v := w.Header().Get("openai-version")
		if v != "2020-10-01" {
			t.Errorf("openai-version = %q, want %q", v, "2020-10-01")
		}
	})

	t.Run("openai-processing-ms is non-negative integer", func(t *testing.T) {
		ms := w.Header().Get("openai-processing-ms")
		if ms == "" {
			t.Fatal("openai-processing-ms is empty")
		}
		val, err := strconv.ParseInt(ms, 10, 64)
		if err != nil {
			t.Errorf("openai-processing-ms = %q, not a valid integer: %v", ms, err)
		}
		if val < 0 {
			t.Errorf("openai-processing-ms = %d, want >= 0", val)
		}
	})
}

func TestCompatHeadersOnErrorResponse(t *testing.T) {
	mw := CompatHeaders()

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))

	req := httptest.NewRequest("GET", "/v1/error", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Header().Get("x-request-id") == "" {
		t.Error("x-request-id missing on error response")
	}
	if w.Header().Get("openai-version") == "" {
		t.Error("openai-version missing on error response")
	}
	if w.Header().Get("openai-processing-ms") == "" {
		t.Error("openai-processing-ms missing on error response")
	}
}

func TestCompatHeadersUniqueRequestIDs(t *testing.T) {
	mw := CompatHeaders()
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	ids := make(map[string]bool)
	for i := 0; i < 10; i++ {
		req := httptest.NewRequest("GET", "/test", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		rid := w.Header().Get("x-request-id")
		if ids[rid] {
			t.Errorf("duplicate x-request-id: %q", rid)
		}
		ids[rid] = true
	}
}

func TestCompatHeadersWithImplicitWriteHeader(t *testing.T) {
	mw := CompatHeaders()

	// Handler that writes body without calling WriteHeader explicitly
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("hello"))
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Header().Get("openai-processing-ms") == "" {
		t.Error("openai-processing-ms missing when WriteHeader not explicitly called")
	}
}

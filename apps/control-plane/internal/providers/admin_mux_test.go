package providers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

// The admin surface used to be mounted with InternalMux(), whose ServeMux
// patterns are absolute ("/internal/providers"). A ServeMux matches on the
// whole request path, so no /api/v1/admin/providers request could ever match a
// pattern and every one fell through to the default 404 — the platform-admin
// provider surface was unreachable in production while the internal one worked.
// These tests pin each prefix to its own mux.

func seededAdminRepo() (*stubRepo, uuid.UUID) {
	id := uuid.New()
	return &stubRepo{
		providers: []Provider{{
			ID:            id,
			Slug:          "openrouter",
			DisplayName:   "OpenRouter",
			BaseURL:       "https://openrouter.ai/api/v1",
			APIKeyEnv:     "OPENROUTER_API_KEY",
			LiteLLMPrefix: "openrouter/",
			Enabled:       true,
		}},
	}, id
}

func TestAdminMuxRoutesAdminPrefix(t *testing.T) {
	repo, seeded := seededAdminRepo()
	mux := NewHandler(NewService(repo)).AdminMux()

	cases := []struct {
		name   string
		method string
		path   string
	}{
		{"list collection", http.MethodGet, "/api/v1/admin/providers"},
		{"get item", http.MethodGet, "/api/v1/admin/providers/" + seeded.String()},
		{"delete item", http.MethodDelete, "/api/v1/admin/providers/" + seeded.String()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, httptest.NewRequest(tc.method, tc.path, nil))
			if rr.Code == http.StatusNotFound {
				t.Fatalf("%s %s: got 404, the admin prefix is not routed; body: %s",
					tc.method, tc.path, rr.Body.String())
			}
		})
	}
}

func TestAdminMuxUnknownIDReturns404(t *testing.T) {
	repo, _ := seededAdminRepo()
	mux := NewHandler(NewService(repo)).AdminMux()

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/admin/providers/"+uuid.New().String(), nil))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("unknown provider id: got %d, want 404", rr.Code)
	}
}

// extractID reads the last path segment, so a well-formed id has to resolve
// identically under either prefix.
func TestAdminAndInternalMuxResolveSameProvider(t *testing.T) {
	repo, seeded := seededAdminRepo()
	h := NewHandler(NewService(repo))

	for _, tc := range []struct {
		name string
		mux  http.Handler
		path string
	}{
		{"admin", h.AdminMux(), "/api/v1/admin/providers/" + seeded.String()},
		{"internal", h.InternalMux(), "/internal/providers/" + seeded.String()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			tc.mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, tc.path, nil))
			if rr.Code != http.StatusOK {
				t.Fatalf("GET %s: got %d, want 200; body: %s", tc.path, rr.Code, rr.Body.String())
			}
			if got := decodeProvider(t, rr.Body).ID; got != seeded {
				t.Fatalf("GET %s: got id %s, want %s", tc.path, got, seeded)
			}
		})
	}
}

func TestInternalMuxDoesNotRouteAdminPrefix(t *testing.T) {
	repo, _ := seededAdminRepo()
	mux := NewHandler(NewService(repo)).InternalMux()

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/admin/providers", nil))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("internal mux must not route the admin prefix: got %d", rr.Code)
	}
}

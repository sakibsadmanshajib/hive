package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/auth"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/featuregate"
)

// Issue #793. The RAG and Cowork feature gates were attached to their routes
// inline in main(). Deleting either attachment kept the entire Go suite green
// because no test ever exercised the registered mux: the featuregate package
// tests build a gate and call it directly, which proves the middleware works
// but proves nothing about whether it is actually on the route.
//
// These tests drive the same registration helpers main() calls, through a real
// http.ServeMux, so the gate, the path, and the pairing between them are all
// under test. Each case names both gates explicitly: a case where the route's
// own gate is off while the other gate is on fails both if the wrapper is
// removed and if the wrong Feature is passed.

// stubControlPlane serves one fixed flag map for every tenant, standing in for
// control-plane's /internal/featuregate/{tenant} endpoint.
func stubControlPlane(t *testing.T, on ...featuregate.Feature) string {
	t.Helper()
	gates := make(map[string]bool, len(on))
	for _, f := range on {
		gates[string(f)] = true
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(featuregate.FlagsResponse{Gates: gates})
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// authedRequest returns a request carrying an authenticated tenant, which is
// what the gate resolves flags for.
func authedRequest(method, path string) *http.Request {
	r := httptest.NewRequestWithContext(context.Background(), method, path, nil)
	return r.WithContext(auth.WithUser(r.Context(), &auth.User{TenantID: uuid.New()}))
}

func TestGatedRouteAttachment(t *testing.T) {
	cases := []struct {
		name string
		// gatesOn is the tenant's live flag set.
		gatesOn []featuregate.Feature
		// authenticated=false drops the tenant from the request context.
		authenticated bool
		path          string
		wantStatus    int
		wantReached   bool
	}{
		{
			name:          "rag reachable when the RAG gate is on",
			gatesOn:       []featuregate.Feature{featuregate.FeatureRAG},
			authenticated: true,
			path:          "/v1/rag/chat",
			wantStatus:    http.StatusOK,
			wantReached:   true,
		},
		{
			// Cowork is on and RAG is off: passes only if /v1/rag/ is gated on
			// FeatureRAG specifically. Deleting the wrapper, or swapping the
			// Feature for Cowork, serves the handler and fails here.
			name:          "rag denied when only the Cowork gate is on",
			gatesOn:       []featuregate.Feature{featuregate.FeatureCowork},
			authenticated: true,
			path:          "/v1/rag/chat",
			wantStatus:    http.StatusForbidden,
			wantReached:   false,
		},
		{
			name:          "rag denied for an unauthenticated request",
			gatesOn:       []featuregate.Feature{featuregate.FeatureRAG},
			authenticated: false,
			path:          "/v1/rag/chat",
			wantStatus:    http.StatusForbidden,
			wantReached:   false,
		},
		{
			name:          "agent tasks reachable when the Cowork gate is on",
			gatesOn:       []featuregate.Feature{featuregate.FeatureCowork},
			authenticated: true,
			path:          "/v1/agent/tasks",
			wantStatus:    http.StatusOK,
			wantReached:   true,
		},
		{
			name:          "agent task subtree reachable when the Cowork gate is on",
			gatesOn:       []featuregate.Feature{featuregate.FeatureCowork},
			authenticated: true,
			path:          "/v1/agent/tasks/01931f00-0000-7000-8000-000000000000",
			wantStatus:    http.StatusOK,
			wantReached:   true,
		},
		{
			name:          "agent tasks denied when only the RAG gate is on",
			gatesOn:       []featuregate.Feature{featuregate.FeatureRAG},
			authenticated: true,
			path:          "/v1/agent/tasks",
			wantStatus:    http.StatusForbidden,
			wantReached:   false,
		},
		{
			// The subtree is a separate mux.Handle call and has its own way of
			// losing the gate.
			name:          "agent task subtree denied when only the RAG gate is on",
			gatesOn:       []featuregate.Feature{featuregate.FeatureRAG},
			authenticated: true,
			path:          "/v1/agent/tasks/01931f00-0000-7000-8000-000000000000",
			wantStatus:    http.StatusForbidden,
			wantReached:   false,
		},
		{
			name:          "agent tasks denied for an unauthenticated request",
			gatesOn:       []featuregate.Feature{featuregate.FeatureCowork},
			authenticated: false,
			path:          "/v1/agent/tasks",
			wantStatus:    http.StatusForbidden,
			wantReached:   false,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			gate := featuregate.New(featuregate.Config{
				ControlPlaneURL: stubControlPlane(t, tc.gatesOn...),
			})

			reached := false
			spy := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reached = true
				w.WriteHeader(http.StatusOK)
			})

			mux := http.NewServeMux()
			registerRAGRoutes(mux, gate, spy)
			registerAgentTaskRoutes(mux, gate, spy)

			var req *http.Request
			if tc.authenticated {
				req = authedRequest(http.MethodGet, tc.path)
			} else {
				req = httptest.NewRequestWithContext(context.Background(), http.MethodGet, tc.path, nil)
			}

			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Errorf("%s: got status %d, want %d", tc.path, rec.Code, tc.wantStatus)
			}
			if reached != tc.wantReached {
				t.Errorf("%s: handler reached = %v, want %v", tc.path, reached, tc.wantReached)
			}
		})
	}
}

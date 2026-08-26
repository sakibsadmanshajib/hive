package main

import (
	"net/http"
	"os"
	"testing"

	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/artifacts"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/featuregate"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/matrix"
)

// TestAssertMatrixCoverage_RealRegistrations drives the real,
// production route-registration functions -- the exact ones main() calls,
// unmodified -- against a routeRecorder, then checks the patterns they
// register against the real committed support-matrix.json.
//
// This is what replaces unsupported_integration_test.go's hand-kept case
// list for the routes it reaches: a new mux.Handle/HandleFunc call inside
// any of these functions needs no test-file edit to be checked here, because
// the check reads the registration code itself, not a parallel list of
// paths someone remembered to type in.
//
// Scope: this test reaches every Hive-proprietary route family (RAG, agent
// tasks, agent schedules, audio voices, artifacts, feature-gate) plus the
// infra and media/file/batch groups -- every registration function main()
// calls with dependencies cheap enough to fake. It does not reach the
// long-stable OpenAI-compatible surface wired inline in main() against real
// provider/DB/routing clients (chat/completions, models, messages, and
// friends): those have never been the site of this defect, and a unit test
// has no business constructing that graph. main()'s own boot-time
// assertMatrixCoverage call (see main.go) is what reaches literally
// everything, provider clients included, every time the process starts.
func TestAssertMatrixCoverage_RealRegistrations(t *testing.T) {
	matrixPath := "../../../../packages/openai-contract/matrix/support-matrix.json"
	if override := os.Getenv("HIVE_MATRIX_PATH_FOR_TEST"); override != "" {
		matrixPath = override
	}
	m, err := matrix.LoadMatrix(matrixPath)
	if err != nil {
		t.Fatalf("loading support matrix from %s: %v", matrixPath, err)
	}

	spy := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	identityMW := func(h http.Handler) http.Handler { return h }
	gate := featuregate.New(featuregate.Config{
		ControlPlaneURL: stubControlPlane(t, featuregate.FeatureRAG, featuregate.FeatureCowork, featuregate.FeatureVoice),
	})

	mux := newRouteRecorder()
	registerInfraRoutes(mux, "openapi.yaml", func() bool { return false })
	registerRAGRoutes(mux, gate, spy)
	registerAgentTaskRoutes(mux, gate, spy)
	registerAgentScheduleRoutes(mux, gate, spy)
	registerAudioVoicesRoute(mux)
	registerMediaFileBatchRoutes(mux, spy, spy, spy, spy, identityMW)
	mux.Handle("/v1/featuregate", featuregate.NewStateHandler(gate))
	artifacts.NewHandler(nil, nil, "", nil, "", nil).Register(mux)

	if err := assertMatrixCoverage(mux.Patterns(), m); err != nil {
		t.Error(err)
	}
}

// TestAssertMatrixCoverage_CatchesAnUnlistedRoute is the negative case: a
// pattern with genuinely zero matrix coverage must be reported, so a
// regression in assertMatrixCoverage itself (e.g. an overly permissive
// prefix match) does not silently stop catching the exact bug this guard
// exists for.
func TestAssertMatrixCoverage_CatchesAnUnlistedRoute(t *testing.T) {
	m, err := matrix.LoadMatrixFromBytes([]byte(`{"version":"0","generated":"","endpoints":[
		{"method":"GET","path":"/v1/models","status":"supported_now","phase":null,"notes":""}
	]}`))
	if err != nil {
		t.Fatalf("loading fixture matrix: %v", err)
	}

	err = assertMatrixCoverage([]string{"/v1/models", "/v1/audio/voices"}, m)
	if err == nil {
		t.Fatal("expected an error for /v1/audio/voices, got nil")
	}
}

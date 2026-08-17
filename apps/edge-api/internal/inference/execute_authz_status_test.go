package inference

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/authz"
)

// upstreamUnavailableOrchestrator builds an Orchestrator whose authorizer
// always fails resolution with authz.ErrUpstreamUnavailable, the shape a
// cold or unreachable control-plane produces. Shared by every execute-path
// test below: PR #903's security review found this exact scenario handled
// correctly by two of the three Authorize call sites in this package and
// missed by the third, so all three now get the same regression guard.
func upstreamUnavailableOrchestrator() *Orchestrator {
	client := &authz.Client{
		ResolveOverride: func(_ context.Context, _ string) (authz.AuthSnapshot, error) {
			return authz.AuthSnapshot{}, fmt.Errorf("authz: fetch: %w: %w", authz.ErrUpstreamUnavailable, context.DeadlineExceeded)
		},
	}
	return &Orchestrator{authorizer: authz.NewAuthorizer(client, nil)}
}

// assertUpstreamUnavailableResponse asserts the OpenAI-style envelope this
// PR's fix requires: 503, not 401, with code upstream_unavailable.
func assertUpstreamUnavailableResponse(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	if w.Code != 503 {
		t.Fatalf("status = %d, want 503 (upstream_unavailable); body: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response body: %v", err)
	}
	errBody, _ := body["error"].(map[string]any)
	if errBody["code"] != "upstream_unavailable" {
		t.Fatalf("error.code = %v, want upstream_unavailable; body: %s", errBody["code"], w.Body.String())
	}
}

// TestExecuteSyncAnswersUpstreamUnavailableNotUnauthorized covers the first
// of the two call sites fixed earlier in PR #903 (executeSync used to carry
// its own copy of the auth-failure status switch, predating the
// upstream_unavailable case entirely).
func TestExecuteSyncAnswersUpstreamUnavailableNotUnauthorized(t *testing.T) {
	orch := upstreamUnavailableOrchestrator()

	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	w := httptest.NewRecorder()

	orch.executeSync(context.Background(), w, req, EndpointChatCompletions, []byte(`{}`), "gpt-4o", NeedFlags{}, 100, nil, nil)

	assertUpstreamUnavailableResponse(t, w)
}

// TestExecuteStreamingAnswersUpstreamUnavailableNotUnauthorized covers the
// second call site fixed earlier in PR #903.
func TestExecuteStreamingAnswersUpstreamUnavailableNotUnauthorized(t *testing.T) {
	orch := upstreamUnavailableOrchestrator()

	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	w := httptest.NewRecorder()

	_ = orch.executeStreaming(context.Background(), w, req, EndpointChatCompletions, []byte(`{}`), "gpt-4o", "gpt-4o", NeedFlags{}, 100, false, nil, nil)

	assertUpstreamUnavailableResponse(t, w)
}

// TestExecuteResponsesStreamingAnswersUpstreamUnavailableNotUnauthorized is
// the regression guard for the third, uncoverted copy the security review
// found at stream_responses.go: streaming /v1/responses still 401'd a cold
// or unreachable control-plane even after the first two call sites were
// fixed, because this one pre-dated the upstream_unavailable case entirely.
func TestExecuteResponsesStreamingAnswersUpstreamUnavailableNotUnauthorized(t *testing.T) {
	orch := upstreamUnavailableOrchestrator()

	req := httptest.NewRequest("POST", "/v1/responses", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	w := httptest.NewRecorder()

	orch.executeResponsesStreaming(context.Background(), w, req, []byte(`{}`), ResponsesRequest{}, "gpt-4o", NeedFlags{}, 100)

	assertUpstreamUnavailableResponse(t, w)
}

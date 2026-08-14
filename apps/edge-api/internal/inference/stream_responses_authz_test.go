package inference

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/authz"
)

// TestExecuteResponsesStreamingAnswersUpstreamUnavailableNotUnauthorized is
// the regression guard for PR #903's security review: executeResponsesStreaming
// carried a third, uncoverted copy of the auth-failure status switch (the
// other two, in executeSync and executeStreaming, were fixed to call
// apierrors.WriteAuthFailure earlier in the same PR). The uncoverted copy
// pre-dated the upstream_unavailable case entirely, so a cold or unreachable
// control-plane still 401'd a valid key on the Responses streaming path even
// after the first two call sites were fixed. Asserts the shared mapper is
// actually used: a resolve failure classified as authz.ErrUpstreamUnavailable
// must answer 503 upstream_unavailable, never 401 invalid_api_key.
func TestExecuteResponsesStreamingAnswersUpstreamUnavailableNotUnauthorized(t *testing.T) {
	client := &authz.Client{
		ResolveOverride: func(_ context.Context, _ string) (authz.AuthSnapshot, error) {
			return authz.AuthSnapshot{}, fmt.Errorf("authz: fetch: %w: %w", authz.ErrUpstreamUnavailable, context.DeadlineExceeded)
		},
	}
	orch := &Orchestrator{authorizer: authz.NewAuthorizer(client, nil)}

	req := httptest.NewRequest("POST", "/v1/responses", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	w := httptest.NewRecorder()

	orch.executeResponsesStreaming(context.Background(), w, req, []byte(`{}`), ResponsesRequest{}, "gpt-4o", NeedFlags{}, 100)

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

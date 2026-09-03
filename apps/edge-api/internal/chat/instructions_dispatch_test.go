package chat_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/auth"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/chat"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/inference"
)

// Issue #1363. These cover the delivery half: that what a person saved is
// actually on the wire toward the model, where in the messages array it sits,
// and that a failure to read it never costs them their turn.
//
// captureUpstream, billedDeps and fakeMemories come from
// memory_dispatch_test.go in this same package.

// fakeInstructions is an InstructionSource returning canned text, recording
// the (tenant, user) it was asked for.
type fakeInstructions struct {
	gotTenant uuid.UUID
	gotUser   uuid.UUID
	text      string
	err       error
}

func (f *fakeInstructions) Instructions(_ context.Context, tenantID, userID uuid.UUID) (string, error) {
	f.gotTenant = tenantID
	f.gotUser = userID
	return f.text, f.err
}

func instructionsRouting(t *testing.T) *httptest.Server {
	t.Helper()
	routing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(inference.SelectRouteResult{
			AliasID:          "hive-fast",
			LiteLLMModelName: "route-groq-fast",
			Provider:         "groq",
			Pricing:          inference.FixedPricing(10_500, 42_000),
			PriceUnit:        inference.PriceUnitTokens,
		})
	}))
	t.Cleanup(routing.Close)
	return routing
}

func dispatchOnce(t *testing.T, deps chat.Deps, tenantID, userID uuid.UUID) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"hive-fast","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(auth.WithUser(req.Context(), &auth.User{
		ID: userID, TenantID: tenantID, Role: "member",
	}))
	rec := httptest.NewRecorder()
	chat.NewDispatch(deps).ServeHTTP(rec, req)
	return rec
}

type wireBody struct {
	Messages []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
}

func decodeWire(t *testing.T, captured []byte) wireBody {
	t.Helper()
	var body wireBody
	require.NoError(t, json.Unmarshal(captured, &body))
	return body
}

func TestDispatchInjectsCustomInstructions(t *testing.T) {
	srv, captured := captureUpstream(t)
	accounting, billing := billedDeps(t)
	instructions := &fakeInstructions{text: "Always answer in British English."}

	userID, tenantID := uuid.New(), uuid.New()
	rec := dispatchOnce(t, chat.Deps{
		Routing:      inference.NewRoutingClient(instructionsRouting(t).URL),
		Accounting:   accounting,
		Billing:      billing,
		Instructions: instructions,
		LiteLLMURL:   srv.URL,
		DeploySHA:    "test",
		Env:          "test",
	}, tenantID, userID)
	require.Equal(t, http.StatusOK, rec.Code)

	// Scope comes from the authenticated principal, never from the body.
	require.Equal(t, tenantID, instructions.gotTenant)
	require.Equal(t, userID, instructions.gotUser)

	body := decodeWire(t, *captured)
	require.Len(t, body.Messages, 2)
	require.Equal(t, "system", body.Messages[0].Role)
	require.Contains(t, body.Messages[0].Content, "Always answer in British English.")
	require.Contains(t, body.Messages[0].Content, "standing instructions")
	require.Equal(t, "user", body.Messages[1].Role)
}

func TestDispatchPutsInstructionsAfterTheSystemMessagesAlreadyPresent(t *testing.T) {
	// The order is the assertion, and it is the opposite of what an earlier
	// revision of this test pinned. The chat container splices the
	// deployment's own prompt in before edge-api sees the body, and that
	// prompt carries identity, citation and refusal guidance. A person's
	// stylistic preference must not sit ahead of it, so the block goes after
	// every leading system message, including the recall block.
	srv, captured := captureUpstream(t)
	accounting, billing := billedDeps(t)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"hive-fast","messages":[`+
			`{"role":"system","content":"You are Hive. Refuse unsafe requests."},`+
			`{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(auth.WithUser(req.Context(), &auth.User{
		ID: uuid.New(), TenantID: uuid.New(), Role: "member",
	}))
	rec := httptest.NewRecorder()
	chat.NewDispatch(chat.Deps{
		Routing:      inference.NewRoutingClient(instructionsRouting(t).URL),
		Accounting:   accounting,
		Billing:      billing,
		Memories:     &fakeMemories{contents: []string{"prefers terse answers"}},
		Instructions: &fakeInstructions{text: "Always answer in British English."},
		LiteLLMURL:   srv.URL,
		DeploySHA:    "test",
		Env:          "test",
	}).ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	body := decodeWire(t, *captured)
	require.Len(t, body.Messages, 4)
	// Recall still prepends, which is #172's pre-existing behaviour and not
	// this issue's to change.
	require.Contains(t, body.Messages[0].Content, "Known about the user:")
	require.Contains(t, body.Messages[1].Content, "Refuse unsafe requests")
	require.Contains(t, body.Messages[2].Content, "Always answer in British English.")
	require.Equal(t, "user", body.Messages[3].Role)
}

func TestDispatchWithNoLeadingSystemMessagePutsInstructionsFirst(t *testing.T) {
	// "After zero leading system messages" is index 0. Without this the
	// insertion point could be off by one on the commonest body shape of all,
	// a bare user turn, and nothing above would notice.
	srv, captured := captureUpstream(t)
	accounting, billing := billedDeps(t)

	rec := dispatchOnce(t, chat.Deps{
		Routing:      inference.NewRoutingClient(instructionsRouting(t).URL),
		Accounting:   accounting,
		Billing:      billing,
		Instructions: &fakeInstructions{text: "Always answer in British English."},
		LiteLLMURL:   srv.URL,
		DeploySHA:    "test",
		Env:          "test",
	}, uuid.New(), uuid.New())
	require.Equal(t, http.StatusOK, rec.Code)

	body := decodeWire(t, *captured)
	require.Len(t, body.Messages, 2)
	require.Equal(t, "system", body.Messages[0].Role)
	require.Contains(t, body.Messages[0].Content, "Always answer in British English.")
	require.Equal(t, "user", body.Messages[1].Role)
}

func TestDispatchWithoutInstructionsAddsNoBlock(t *testing.T) {
	for _, tt := range []struct {
		name string
		deps chat.InstructionSource
	}{
		{"source not wired at all", nil},
		{"person has written none", &fakeInstructions{text: ""}},
		{"person wrote only whitespace", &fakeInstructions{text: "   \n  "}},
		{"read failed", &fakeInstructions{err: errors.New("instructions store down")}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv, captured := captureUpstream(t)
			accounting, billing := billedDeps(t)

			rec := dispatchOnce(t, chat.Deps{
				Routing:      inference.NewRoutingClient(instructionsRouting(t).URL),
				Accounting:   accounting,
				Billing:      billing,
				Instructions: tt.deps,
				LiteLLMURL:   srv.URL,
				DeploySHA:    "test",
				Env:          "test",
			}, uuid.New(), uuid.New())

			// The turn is served either way: an unreadable preference must
			// never cost someone their message.
			require.Equal(t, http.StatusOK, rec.Code)
			body := decodeWire(t, *captured)
			require.Len(t, body.Messages, 1)
			require.Equal(t, "user", body.Messages[0].Role)
		})
	}
}

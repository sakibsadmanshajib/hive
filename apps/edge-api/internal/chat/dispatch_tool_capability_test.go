package chat_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/auth"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/chat"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/inference"
	"github.com/stretchr/testify/require"
)

// The session chat path never set RequireToolCapable, so a tool payload from
// the chat surface was dispatched with no capability check at all. These hold
// the flag against the body, in both directions.
//
// The routing stub answers 404 after recording what it was asked, which stops
// dispatch immediately after the call under test. That keeps these off the
// database the happy-path tests need, and the thing being asserted is the
// SelectRoute input, not what happens to the turn afterwards.

func recordingRoutingClient(t *testing.T, status int, body string) (*inference.RoutingClient, *inference.SelectRouteInput) {
	t.Helper()
	var seen inference.SelectRouteInput
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&seen)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return inference.NewRoutingClient(srv.URL), &seen
}

func dispatchRequest(t *testing.T, handler http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(auth.WithUser(req.Context(), &auth.User{
		ID:       uuid.New(),
		TenantID: uuid.New(),
		Role:     "member",
		Email:    "tools@example.test",
	}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestDispatchSetsRequireToolCapableForEveryToolShapedField(t *testing.T) {
	bodies := map[string]string{
		"tools":               `{"model":"hive-free","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"web_search"}}]}`,
		"tool_choice":         `{"model":"hive-free","messages":[{"role":"user","content":"hi"}],"tool_choice":"auto"}`,
		"response_format":     `{"model":"hive-free","messages":[{"role":"user","content":"hi"}],"response_format":{"type":"json_object"}}`,
		"functions":           `{"model":"hive-free","messages":[{"role":"user","content":"hi"}],"functions":[{"name":"f"}]}`,
		"function_call":       `{"model":"hive-free","messages":[{"role":"user","content":"hi"}],"function_call":"auto"}`,
		"parallel_tool_calls": `{"model":"hive-free","messages":[{"role":"user","content":"hi"}],"parallel_tool_calls":true}`,
		"empty tools array":   `{"model":"hive-free","messages":[{"role":"user","content":"hi"}],"tools":[]}`,
	}

	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			routing, seen := recordingRoutingClient(t, http.StatusNotFound, `{"error":"routing: alias not found"}`)
			handler := chat.NewDispatch(chat.Deps{Routing: routing})

			dispatchRequest(t, handler, body)

			require.True(t, seen.RequireToolCapable,
				"a body carrying %s was dispatched without the tool capability requirement, so it can land on a route that answers 404 No endpoints found that support tool use", name)
		})
	}
}

// The other direction, and the one that matters for every chat turn that never
// touches a tool: a plain body must not carry the flag. If it did, advertising
// tools would not be the only thing narrowing route selection: every turn would.
func TestDispatchLeavesRequireToolCapableUnsetForAPlainTurn(t *testing.T) {
	plainBodies := []string{
		`{"model":"hive-free","messages":[{"role":"user","content":"hi"}]}`,
		`{"model":"hive-free","messages":[{"role":"user","content":"hi"}],"tools":null}`,
		`{"model":"hive-free","messages":[{"role":"user","content":"hi"}],"response_format":null}`,
	}

	for _, body := range plainBodies {
		routing, seen := recordingRoutingClient(t, http.StatusNotFound, `{"error":"routing: alias not found"}`)
		handler := chat.NewDispatch(chat.Deps{Routing: routing})

		dispatchRequest(t, handler, body)

		require.False(t, seen.RequireToolCapable,
			"a plain turn asked for a tool-capable route: %s", body)
	}
}

// A tool block that reaches an alias the catalog says cannot serve one is a
// permanent capability mismatch, not a routing outage. Before this it fell into
// the default arm and answered 503 routing unavailable, which sends the reader
// looking for an outage that is not happening.
func TestDispatchReportsAnIncapableAliasAsABadRequestNotAnOutage(t *testing.T) {
	routing, _ := recordingRoutingClient(t, http.StatusUnprocessableEntity,
		`{"error":"routing: no tool-capable route: alias hive-free has no tool-capable routes"}`)
	handler := chat.NewDispatch(chat.Deps{Routing: routing})

	rec := dispatchRequest(t, handler,
		`{"model":"hive-free","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"web_search"}}]}`)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "cannot use tools")
	// Provider blindness: the upstream error text names routes and providers,
	// and none of it may reach the customer.
	for _, leak := range []string{"route-", "openrouter", "groq", "litellm"} {
		require.NotContains(t, strings.ToLower(rec.Body.String()), leak)
	}
}

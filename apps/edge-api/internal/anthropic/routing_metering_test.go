package anthropic_test

// These tests wire POST /v1/messages to the real handler chains it delegates to
// (the JWT chat dispatcher and the API-key inference orchestrator), fronted by a
// fake control-plane and a fake LiteLLM. They exist because the surface used to
// POST straight to LiteLLM: since a LiteLLM model name IS a route id, that let a
// caller name route-groq-fast and skip per-tenant model entitlement, the API-key
// alias allowlist, and credit metering in one move.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/anthropic"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/auth"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/authz"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/chat"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/inference"
)

// controlPlane is a fake control-plane covering the two internal surfaces the
// inference path calls: route selection and credit accounting.
type controlPlane struct {
	mu sync.Mutex

	// routing behaviour
	selectStatus int    // non-zero to refuse every selection with this status
	knownAlias   string // the only alias that resolves; anything else 404s
	routeName    string // LiteLLM model name the alias resolves to

	// recorded calls
	selectInputs []inference.SelectRouteInput
	reservations []inference.CreateReservationInput
	finalized    []inference.FinalizeReservationInput
	released     []inference.ReleaseReservationInput
	events       []map[string]any
}

func (c *controlPlane) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/internal/routing/select", func(w http.ResponseWriter, r *http.Request) {
		var in inference.SelectRouteInput
		_ = json.NewDecoder(r.Body).Decode(&in)
		c.mu.Lock()
		c.selectInputs = append(c.selectInputs, in)
		status, known, route := c.selectStatus, c.knownAlias, c.routeName
		c.mu.Unlock()

		if status != 0 {
			w.WriteHeader(status)
			_, _ = fmt.Fprintf(w, `{"error":"refused"}`)
			return
		}
		if in.AliasID != known {
			// Route ids live in model_routes, aliases in alias_route_policies.
			// A caller naming a route therefore finds no alias policy at all.
			w.WriteHeader(http.StatusNotFound)
			_, _ = fmt.Fprintf(w, `{"error":"routing: alias not found: %s"}`, in.AliasID)
			return
		}
		_ = json.NewEncoder(w).Encode(inference.SelectRouteResult{
			AliasID:          in.AliasID,
			RouteID:          "route-test-primary",
			LiteLLMModelName: route,
			Provider:         "test-provider",
			// The real endpoint always carries the alias's catalog price and an
			// explicit unit, and the settlement charge is derived from them
			// (#688). These are hive-fast's rows as of migration
			// 20260801_01_alias_pricing_correction.sql, in credits per million
			// tokens, pinned on purpose rather than tracked against later
			// repricings (see the note at the top of
			// apps/edge-api/internal/inference/settle_from_catalog_test.go).
			Pricing:   inference.SelectRoutePricing{InputPriceCredits: 10_500, OutputPriceCredits: 42_000},
			PriceUnit: inference.PriceUnitTokens,
		})
	})
	mux.HandleFunc("/internal/accounting/reservations", func(w http.ResponseWriter, r *http.Request) {
		var in inference.CreateReservationInput
		_ = json.NewDecoder(r.Body).Decode(&in)
		c.mu.Lock()
		c.reservations = append(c.reservations, in)
		id := fmt.Sprintf("res_%d", len(c.reservations))
		c.mu.Unlock()
		_ = json.NewEncoder(w).Encode(inference.ReservationResult{
			ID:               id,
			AccountID:        in.AccountID,
			Status:           "active",
			EstimatedCredits: in.EstimatedCredits,
		})
	})
	mux.HandleFunc("/internal/accounting/reservations/finalize", func(w http.ResponseWriter, r *http.Request) {
		var in inference.FinalizeReservationInput
		_ = json.NewDecoder(r.Body).Decode(&in)
		c.mu.Lock()
		c.finalized = append(c.finalized, in)
		c.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/internal/accounting/reservations/release", func(w http.ResponseWriter, r *http.Request) {
		var in inference.ReleaseReservationInput
		_ = json.NewDecoder(r.Body).Decode(&in)
		c.mu.Lock()
		c.released = append(c.released, in)
		c.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/internal/usage/attempts", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(inference.AttemptResult{ID: "att_1", Status: "dispatching"})
	})
	mux.HandleFunc("/internal/usage/events", func(w http.ResponseWriter, r *http.Request) {
		event := map[string]any{}
		_ = json.NewDecoder(r.Body).Decode(&event)
		c.mu.Lock()
		c.events = append(c.events, event)
		c.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})
	return mux
}

func (c *controlPlane) snapshot() ([]inference.SelectRouteInput, []inference.CreateReservationInput,
	[]inference.FinalizeReservationInput, []inference.ReleaseReservationInput) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.selectInputs, c.reservations, c.finalized, c.released
}

// liteLLM is a fake LiteLLM proxy. It records every dispatch so a test can
// prove a refused request never reached an upstream model.
type liteLLM struct {
	mu sync.Mutex

	status int      // response status, defaults to 200
	body   string   // raw body for a non-2xx response
	chunks []string // OpenAI SSE chunk payloads for a 2xx response

	calls  int
	models []string
	bodies []string
}

func (l *liteLLM) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		parsed := map[string]any{}
		_ = json.Unmarshal(raw, &parsed)
		model, _ := parsed["model"].(string)

		l.mu.Lock()
		l.calls++
		l.models = append(l.models, model)
		l.bodies = append(l.bodies, string(raw))
		status, body, chunks := l.status, l.body, l.chunks
		l.mu.Unlock()

		if status != 0 && (status < 200 || status > 299) {
			w.WriteHeader(status)
			_, _ = fmt.Fprint(w, body)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		for _, chunk := range chunks {
			_, _ = fmt.Fprintf(w, "data: %s\n\n", chunk)
			if flusher != nil {
				flusher.Flush()
			}
		}
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
		if flusher != nil {
			flusher.Flush()
		}
	})
}

func (l *liteLLM) observed() (int, []string, []string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.calls, l.models, l.bodies
}

func defaultChunks() []string {
	return []string{
		`{"id":"chatcmpl-live","model":"route-test-primary","choices":[{"index":0,"delta":{"role":"assistant","content":"Hello!"}}]}`,
		`{"id":"chatcmpl-live","model":"route-test-primary","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18}}`,
	}
}

// newSessionChain builds the JWT-session half of the /v1/chat/completions chain
// (internal/chat dispatch) on top of the fakes.
func newSessionChain(t *testing.T, cp *controlPlane, ll *liteLLM) http.Handler {
	t.Helper()
	cpSrv := httptest.NewServer(cp.handler())
	t.Cleanup(cpSrv.Close)
	llSrv := httptest.NewServer(ll.handler())
	t.Cleanup(llSrv.Close)

	return chat.NewDispatch(chat.Deps{
		Routing:    inference.NewRoutingClient(cpSrv.URL),
		LiteLLMURL: llSrv.URL,
		LiteLLMKey: "test-master-key",
	})
}

// newAPIKeyChain builds the API-key half of the /v1/chat/completions chain (the
// inference orchestrator, which owns credit reservation) on top of the fakes.
// The auth snapshot is injected through the Client's documented test hook so no
// Redis or control-plane key resolution is needed.
func newAPIKeyChain(t *testing.T, cp *controlPlane, ll *liteLLM, snapshot authz.AuthSnapshot) http.Handler {
	t.Helper()
	cpSrv := httptest.NewServer(cp.handler())
	t.Cleanup(cpSrv.Close)
	llSrv := httptest.NewServer(ll.handler())
	t.Cleanup(llSrv.Close)

	authClient := &authz.Client{
		ResolveOverride: func(ctx context.Context, rawToken string) (authz.AuthSnapshot, error) {
			return snapshot, nil
		},
	}
	orchestrator := inference.NewOrchestrator(
		authz.NewAuthorizer(authClient, nil),
		inference.NewRoutingClient(cpSrv.URL),
		inference.NewAccountingClient(cpSrv.URL),
		inference.NewLiteLLMClient(llSrv.URL, "test-master-key"),
	)
	return inference.NewHandler(orchestrator)
}

func activeSnapshot() authz.AuthSnapshot {
	return authz.AuthSnapshot{
		KeyID:          "key_1",
		AccountID:      "acct_1",
		TenantID:       "22222222-2222-2222-2222-222222222222",
		Status:         "active",
		AllowAllModels: true,
		BudgetKind:     "none",
	}
}

func sessionRequest(t *testing.T, body string) *http.Request {
	t.Helper()
	return newAuthedRequest(t, body)
}

func apiKeyRequest(t *testing.T, body string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer hk_test_key")
	return req
}

// A raw LiteLLM route id is not an alias, so it resolves to nothing and the
// request must be refused before any upstream dispatch happens.
func TestMessages_RawLiteLLMRouteIDIsRefusedAndNeverDispatched(t *testing.T) {
	cp := &controlPlane{knownAlias: "hive-fast", routeName: "test/model-a"}
	ll := &liteLLM{chunks: defaultChunks()}
	h := anthropic.NewHandler(anthropic.Deps{OpenAIChat: newSessionChain(t, cp, ll)})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, sessionRequest(t,
		`{"model":"route-groq-fast","messages":[{"role":"user","content":"hi"}],"max_tokens":16}`))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status: want 404 got %d body=%s", rec.Code, rec.Body.String())
	}
	if calls, _, _ := ll.observed(); calls != 0 {
		t.Errorf("upstream dispatches for a raw route id: want 0 got %d", calls)
	}
	body := strings.ToLower(rec.Body.String())
	for _, leak := range []string{"route-groq-fast", "route-test-primary", "groq", "litellm", "openrouter"} {
		if strings.Contains(body, leak) {
			t.Errorf("refusal leaked %q: %s", leak, rec.Body.String())
		}
	}
}

func TestMessages_RawRouteIDIsAlsoRefusedOnStreamingRequests(t *testing.T) {
	cp := &controlPlane{knownAlias: "hive-fast", routeName: "test/model-a"}
	ll := &liteLLM{chunks: defaultChunks()}
	h := anthropic.NewHandler(anthropic.Deps{OpenAIChat: newSessionChain(t, cp, ll)})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, sessionRequest(t,
		`{"model":"route-openrouter-default","messages":[{"role":"user","content":"hi"}],"max_tokens":16,"stream":true}`))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status: want 404 got %d body=%s", rec.Code, rec.Body.String())
	}
	if calls, _, _ := ll.observed(); calls != 0 {
		t.Errorf("upstream dispatches for a raw route id: want 0 got %d", calls)
	}
	if strings.Contains(rec.Body.String(), "message_start") {
		t.Error("refusal must not be delivered as a stream")
	}
}

// An alias the tenant is not entitled to is an administrative verdict: 403, and
// the message names only what the caller already asked for.
func TestMessages_UnentitledAliasIsRefusedProviderBlind(t *testing.T) {
	cp := &controlPlane{selectStatus: http.StatusForbidden, knownAlias: "hive-fast", routeName: "test/model-a"}
	ll := &liteLLM{chunks: defaultChunks()}
	h := anthropic.NewHandler(anthropic.Deps{OpenAIChat: newSessionChain(t, cp, ll)})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, sessionRequest(t,
		`{"model":"hive-premium","messages":[{"role":"user","content":"hi"}],"max_tokens":16}`))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status: want 403 got %d body=%s", rec.Code, rec.Body.String())
	}
	if calls, _, _ := ll.observed(); calls != 0 {
		t.Errorf("upstream dispatches for an unentitled alias: want 0 got %d", calls)
	}
	if !strings.Contains(rec.Body.String(), "model not available for this workspace") {
		t.Errorf("unexpected refusal message: %s", rec.Body.String())
	}
	body := strings.ToLower(rec.Body.String())
	for _, leak := range []string{"route-", "groq", "openrouter", "litellm", "hive-fast"} {
		if strings.Contains(body, leak) {
			t.Errorf("refusal leaked %q: %s", leak, rec.Body.String())
		}
	}
}

func TestMessages_EntitledAliasResolvesRouteBeforeDispatch(t *testing.T) {
	cp := &controlPlane{knownAlias: "hive-fast", routeName: "test/model-a"}
	ll := &liteLLM{chunks: defaultChunks()}
	h := anthropic.NewHandler(anthropic.Deps{OpenAIChat: newSessionChain(t, cp, ll)})

	req := sessionRequest(t, `{"model":"hive-fast","messages":[{"role":"user","content":"hi"}],"max_tokens":32,`+
		`"tools":[{"name":"lookup","input_schema":{"type":"object","properties":{}}}]}`)
	user, _ := auth.UserFrom(req.Context())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200 got %d body=%s", rec.Code, rec.Body.String())
	}

	selects, _, _, _ := cp.snapshot()
	if len(selects) != 1 {
		t.Fatalf("SelectRoute calls: want 1 got %d", len(selects))
	}
	if selects[0].AliasID != "hive-fast" {
		t.Errorf("SelectRoute alias: want hive-fast got %q", selects[0].AliasID)
	}
	// The tenant must reach route selection, since that is where per-tenant
	// model entitlement is enforced.
	if selects[0].TenantID != user.TenantID.String() {
		t.Errorf("SelectRoute tenant: want %s got %q", user.TenantID, selects[0].TenantID)
	}

	calls, models, bodies := ll.observed()
	if calls != 1 {
		t.Fatalf("upstream dispatches: want 1 got %d", calls)
	}
	if models[0] != "test/model-a" {
		t.Errorf("upstream model: want the resolved route name test/model-a got %q", models[0])
	}
	// Parameters the caller sent must survive the alias rewrite.
	if !strings.Contains(bodies[0], `"max_tokens":32`) || !strings.Contains(bodies[0], `"tools"`) {
		t.Errorf("upstream body lost request parameters: %s", bodies[0])
	}

	var got anthropic.MessagesResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Model != "hive-fast" {
		t.Errorf("model echoed: want hive-fast got %q", got.Model)
	}
	if len(got.Content) != 1 || got.Content[0].Text != "Hello!" {
		t.Errorf("content: %+v", got.Content)
	}
	if got.Usage.InputTokens != 11 || got.Usage.OutputTokens != 7 {
		t.Errorf("usage: %+v", got.Usage)
	}
}

func TestMessages_UpstreamFailureIsProviderBlindThroughTheChain(t *testing.T) {
	cp := &controlPlane{knownAlias: "hive-fast", routeName: "test/model-a"}
	ll := &liteLLM{
		status: http.StatusTooManyRequests,
		body:   `{"error":{"message":"groq rate limit hit via openrouter/auto; litellm route-fast; anthropic backend"}}`,
	}
	h := anthropic.NewHandler(anthropic.Deps{OpenAIChat: newSessionChain(t, cp, ll)})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, sessionRequest(t,
		`{"model":"hive-fast","messages":[{"role":"user","content":"hi"}],"max_tokens":16}`))

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status: want 429 got %d body=%s", rec.Code, rec.Body.String())
	}
	body := strings.ToLower(rec.Body.String())
	leakTerms := []string{
		"openai", "anthropic", "openrouter", "groq", "ollama", "vllm", "sglang",
		"nim", "litellm", "google", "gemini", "mistral", "cohere", "cerebras",
		"deepseek", "xai", "together", "fireworks", "replicate", "perplexity",
		"route-",
	}
	for _, term := range leakTerms {
		if strings.Contains(body, term) {
			t.Errorf("provider-blind violation: %q leaked in response body: %s", term, rec.Body.String())
		}
	}
}

// Metering: an API-key call on this surface must hold credits before dispatch
// and settle them at the real token count afterwards.
func TestMessages_APIKeyPathReservesAndSettlesCredits(t *testing.T) {
	cp := &controlPlane{knownAlias: "hive-fast", routeName: "test/model-a"}
	ll := &liteLLM{chunks: defaultChunks()}
	h := anthropic.NewHandler(anthropic.Deps{
		OpenAIChat: newAPIKeyChain(t, cp, ll, activeSnapshot()),
	})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, apiKeyRequest(t,
		`{"model":"hive-fast","messages":[{"role":"user","content":"hi"}],"max_tokens":16}`))

	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200 got %d body=%s", rec.Code, rec.Body.String())
	}

	_, reservations, finalized, released := cp.snapshot()
	if len(reservations) != 1 {
		t.Fatalf("reservations: want 1 got %d", len(reservations))
	}
	if reservations[0].ModelAlias != "hive-fast" {
		t.Errorf("reservation alias: want hive-fast got %q", reservations[0].ModelAlias)
	}
	if reservations[0].AccountID != "acct_1" {
		t.Errorf("reservation account: want acct_1 got %q", reservations[0].AccountID)
	}
	if reservations[0].EstimatedCredits <= 0 {
		t.Errorf("reservation must hold credits before dispatch, got %d", reservations[0].EstimatedCredits)
	}
	if len(finalized) != 1 {
		t.Fatalf("finalized reservations: want 1 got %d", len(finalized))
	}
	// 11 input + 7 output tokens at hive-fast's catalog price is 0.41 credits,
	// which the never-free floor lifts to 1. It is deliberately NOT 18, the
	// token total this used to settle at (#688); the catalog-price bound at
	// thousands of tokens lives in
	// apps/edge-api/internal/inference/settle_from_catalog_test.go.
	if finalized[0].ActualCredits != 1 {
		t.Errorf("settled credits: want the catalog price for 11 input + 7 output tokens got %d", finalized[0].ActualCredits)
	}
	if finalized[0].ActualCredits == 18 {
		t.Error("settled credits: 18 is the raw token count, not a catalog-derived charge (#688)")
	}
	if !finalized[0].TerminalUsageConfirmed {
		t.Error("settlement must be marked terminal-usage-confirmed when upstream reported usage")
	}
	if finalized[0].Status != "completed" {
		t.Errorf("settlement status: want completed got %q", finalized[0].Status)
	}
	if len(released) != 0 {
		t.Errorf("no reservation should be released on success, got %d", len(released))
	}
}

func TestMessages_APIKeyStreamingPathReservesAndSettlesCredits(t *testing.T) {
	cp := &controlPlane{knownAlias: "hive-fast", routeName: "test/model-a"}
	ll := &liteLLM{chunks: defaultChunks()}
	h := anthropic.NewHandler(anthropic.Deps{
		OpenAIChat: newAPIKeyChain(t, cp, ll, activeSnapshot()),
	})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, apiKeyRequest(t,
		`{"model":"hive-fast","messages":[{"role":"user","content":"hi"}],"max_tokens":16,"stream":true}`))

	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200 got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "message_stop") {
		t.Errorf("streamed response incomplete: %s", rec.Body.String())
	}

	_, reservations, finalized, _ := cp.snapshot()
	if len(reservations) != 1 || len(finalized) != 1 {
		t.Fatalf("reservations/settlements: want 1/1 got %d/%d", len(reservations), len(finalized))
	}
	if finalized[0].ActualCredits != 1 {
		t.Errorf("settled credits: want the catalog price for 11 input + 7 output tokens got %d", finalized[0].ActualCredits)
	}
}

func TestMessages_APIKeyPathReleasesReservationOnUpstreamFailure(t *testing.T) {
	cp := &controlPlane{knownAlias: "hive-fast", routeName: "test/model-a"}
	ll := &liteLLM{status: http.StatusInternalServerError, body: `{"error":{"message":"upstream exploded"}}`}
	h := anthropic.NewHandler(anthropic.Deps{
		OpenAIChat: newAPIKeyChain(t, cp, ll, activeSnapshot()),
	})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, apiKeyRequest(t,
		`{"model":"hive-fast","messages":[{"role":"user","content":"hi"}],"max_tokens":16}`))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status: want 500 got %d body=%s", rec.Code, rec.Body.String())
	}

	_, reservations, finalized, released := cp.snapshot()
	if len(reservations) != 1 {
		t.Fatalf("reservations: want 1 got %d", len(reservations))
	}
	if len(released) != 1 {
		t.Fatalf("released reservations: want 1 got %d", len(released))
	}
	if released[0].ReservationID != "res_1" || released[0].Reason != "upstream_error" {
		t.Errorf("release: %+v", released[0])
	}
	if len(finalized) != 0 {
		t.Errorf("a failed request must not settle credits, got %d settlements", len(finalized))
	}
}

// The API-key alias allowlist is the other control the direct dispatch skipped.
func TestMessages_APIKeyPathRefusesAliasOutsideKeyPolicy(t *testing.T) {
	cp := &controlPlane{knownAlias: "hive-fast", routeName: "test/model-a"}
	ll := &liteLLM{chunks: defaultChunks()}
	snapshot := activeSnapshot()
	snapshot.AllowAllModels = false
	snapshot.AllowedAliases = []string{"hive-basic"}
	h := anthropic.NewHandler(anthropic.Deps{OpenAIChat: newAPIKeyChain(t, cp, ll, snapshot)})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, apiKeyRequest(t,
		`{"model":"hive-fast","messages":[{"role":"user","content":"hi"}],"max_tokens":16}`))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status: want 404 got %d body=%s", rec.Code, rec.Body.String())
	}
	if calls, _, _ := ll.observed(); calls != 0 {
		t.Errorf("upstream dispatches for a disallowed alias: want 0 got %d", calls)
	}
	_, reservations, _, _ := cp.snapshot()
	if len(reservations) != 0 {
		t.Errorf("a refused request must not reserve credits, got %d", len(reservations))
	}
}

func TestMessages_APIKeyPathRawRouteIDIsRefusedWithoutReservation(t *testing.T) {
	cp := &controlPlane{knownAlias: "hive-fast", routeName: "test/model-a"}
	ll := &liteLLM{chunks: defaultChunks()}
	h := anthropic.NewHandler(anthropic.Deps{OpenAIChat: newAPIKeyChain(t, cp, ll, activeSnapshot())})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, apiKeyRequest(t,
		`{"model":"route-groq-fast","messages":[{"role":"user","content":"hi"}],"max_tokens":16}`))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status: want 404 got %d body=%s", rec.Code, rec.Body.String())
	}
	if calls, _, _ := ll.observed(); calls != 0 {
		t.Errorf("upstream dispatches for a raw route id: want 0 got %d", calls)
	}
	_, reservations, _, _ := cp.snapshot()
	if len(reservations) != 0 {
		t.Errorf("a refused request must not reserve credits, got %d", len(reservations))
	}
	// The OpenAI-compatible 404 quotes the model the caller itself supplied, and
	// the status is identical whether or not that string names a real route, so it
	// is no oracle. What must never appear is anything from our own route table.
	body := strings.ToLower(rec.Body.String())
	for _, leak := range []string{"route-test-primary", "test/model-a", "test-provider", "litellm"} {
		if strings.Contains(body, leak) {
			t.Errorf("refusal leaked %q from the route table: %s", leak, rec.Body.String())
		}
	}
}

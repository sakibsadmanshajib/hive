package inference

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	apierrors "github.com/sakibsadmanshajib/hive/apps/edge-api/internal/errors"
)

// handleChatCompletions handles POST /v1/chat/completions.
func handleChatCompletions(o *Orchestrator, w http.ResponseWriter, r *http.Request) {
	body, ok := readLimitedBody(w, r)
	if !ok {
		return
	}

	var req ChatCompletionRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeInvalidBodyError(w)
		return
	}

	if req.Model == "" {
		writeMissingFieldError(w, "model")
		return
	}
	if len(req.Messages) == 0 {
		writeMissingFieldError(w, "messages")
		return
	}
	// An empty array, a non-object entry and an unknown role are all the
	// caller payload, so they are refused here instead of being forwarded and
	// coming back as an availability verdict about the model (#1348).
	if !validateChatMessages(w, req.Messages) {
		return
	}
	// n other than 1 is refused rather than silently truncated to one choice
	// (issue #1283). See writeUnsupportedChoiceCountError.
	if unsupportedChoiceCount(req.N) {
		writeUnsupportedChoiceCountError(w)
		return
	}

	// Detect tool-calling / structured-output parameters (issue #118).
	// If present, probe whether the alias has at least one tool-capable route.
	// Return 400 only when no capable route exists; otherwise pass through.
	toolParam := firstToolParam(&req)
	if toolParam != "" {
		if blocked := guardToolCapability(r.Context(), o, w, req.Model, toolParam); blocked {
			return
		}
	}

	needFlags := NeedFlags{
		NeedChatCompletions: true,
		NeedStreaming:        req.Stream,
		NeedReasoning:        req.ReasoningEffort != nil,
		RequireToolCapable:  toolParam != "",
	}

	if req.Stream {
		includeUsage := req.StreamOptions != nil && req.StreamOptions.IncludeUsage
		o.executeStreaming(r.Context(), w, r, EndpointChatCompletions, body, req.Model, req.Model, needFlags, DefaultHoldText, includeUsage, req.ReasoningEffort, o.litellm.ChatCompletionStream)
		return
	}

	o.executeSync(r.Context(), w, r, EndpointChatCompletions, body, req.Model, needFlags, DefaultHoldText,
		o.litellm.ChatCompletion, normalizeChatCompletion)
}

// normalizeChatCompletion normalizes a LiteLLM chat completion response.
func normalizeChatCompletion(respBody []byte, aliasID string) ([]byte, *UsageResponse, error) {
	var resp ChatCompletionResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, nil, err
	}

	// Mint a gateway-owned id and drop system_fingerprint: both are upstream
	// identity leaks (see mintCompletionID). The upstream id is kept in
	// upstreamID purely for the usage-clamp log line below; nothing that
	// matters for billing or correlation reads resp.ID.
	upstreamID := resp.ID
	resp.ID = mintCompletionID("chatcmpl")
	resp.SystemFingerprint = nil

	resp.Model = aliasID
	resp.Object = "chat.completion"

	// Every OpenAI SDK generates message.content as a plain string type and
	// dereferences it unconditionally; content is nullable ONLY when the
	// message carries a tool call (OpenAI's own contract). A reasoning-heavy
	// upstream that spends its whole token budget on hidden reasoning and
	// returns finish_reason=length with content omitted otherwise leaks a
	// bare `null` straight through to every client. Caught live on the
	// hive-free pool (deploy run 32879931588, PR #1115/#1155): one of its
	// four load-balanced members returned null content on a plain, tool-free
	// "Say hello" prompt. This gateway is provider-blind by design (no pool
	// member's shape should leak to the client), so the fix belongs at this
	// normalization boundary, not in any one provider's route config.
	for i := range resp.Choices {
		coerceNullContent(&resp.Choices[i].Message)
	}

	clampZeroCompletionUsage(resp.Usage, chatChoiceTexts(resp.Choices), upstreamID, aliasID, EndpointChatCompletions)

	// usage.prompt_tokens_details and usage.completion_tokens_details are
	// part of the shape an OpenAI SDK caller is entitled to, and an upstream
	// that omits them makes the field come and go between two identical
	// requests. That is exactly what the live conformance suite saw: the same
	// assertion on the same alias passed in one run and failed in the next.
	// normalizeReasoningUsage already existed for this and had no caller at
	// all, so nothing was ever normalized. Zero-filling here, before the
	// marshal, is what makes the field a promise instead of a coin flip.
	normalizeReasoningUsage(resp.Usage)

	normalized, err := json.Marshal(resp)
	if err != nil {
		return nil, nil, err
	}

	return normalized, resp.Usage, nil
}

// coerceNullContent enforces the OpenAI contract that message.content is a
// string whenever the message carries no tool call and no legacy function
// call. content is nullable ONLY alongside tool_calls or function_call; every
// other shape must be a string, per the SDKs this gateway's clients actually
// use. Leaves the message untouched otherwise, since a genuine tool-call
// message with null content is spec-correct and must not be rewritten.
func coerceNullContent(msg *ChatCompletionMessage) {
	if msg.Content != nil {
		return
	}
	if rawFieldPresent(msg.ToolCalls) || rawFieldPresent(msg.FunctionCall) {
		return
	}
	empty := ""
	msg.Content = &empty
}

// rawFieldPresent reports whether a json.RawMessage field is present and not
// the JSON literal "null".
func rawFieldPresent(f json.RawMessage) bool {
	return len(f) > 0 && string(f) != "null"
}

// toolParamNames is the ordered list of request fields that make a request
// tool-calling or structured-output shaped, and therefore require a
// tool-capable route. Ordered, and in the same order as firstToolParam decides,
// because the name is reported to the caller and the first match wins.
//
// Read by the fallback arm of ToolParamInBody only. The primary arm delegates
// to firstToolParam itself, so the two surfaces cannot come to different
// verdicts about the same body by construction rather than by two lists being
// kept in sync.
var toolParamNames = []string{
	"tools",
	"tool_choice",
	"response_format",
	"functions",
	"function_call",
	"parallel_tool_calls",
}

// ToolParamInBody returns the name of the first tool-calling or structured-output
// parameter present in a raw OpenAI-shaped chat request body, or "" if none are.
//
// It DELEGATES to firstToolParam, and that is load bearing rather than tidy.
// An earlier version decoded into a field map with exact keys, which looked
// equivalent and was not: encoding/json matches struct field names CASE
// INSENSITIVELY, so a body spelling `{"Tools": [...]}` populates req.Tools and
// reads tool-shaped on the API-key surface, while an exact-key map lookup reads
// it as carrying nothing. The session chat surface would then have dispatched
// it with no capability check at all, which is the exact hole this function
// exists to close, reachable by changing one letter.
//
// The fallback arm covers the case the map decode was originally chosen for: a
// body that fails the typed decode for some unrelated reason (a wrongly typed
// `stream` or `n`, say) must not read as plain and slip the gate. A field map
// decodes anything that is a JSON object at all, and the key comparison there
// is case folded to match what the typed decoder would have done. That arm is
// unreachable from the API-key surface, which rejects such a body outright, so
// there is no second verdict for it to disagree with.
//
// An input that is not a JSON object cannot carry a tool block either, so "" is
// the honest answer there.
// KNOWN BLIND SPOT, recorded rather than closed, because closing it costs more
// than it buys and the cost lands on every plain chat turn.
//
// A body spelling one parameter twice still reads as plain here:
//
//	{"tools":[{"type":"function"}],"Tools":null}   this returns ""
//
// The typed decoder resolves both spellings onto one field and lets the last
// one win, so the null overwrites the real array, while a case-sensitive
// decoder downstream still finds a genuine `tools` array under the exact key.
//
// Three reasons it is recorded and not fixed. It is NOT a divergence between
// the two surfaces, which is the property this function was reworked to
// restore: firstToolParam answers "" for the same body, so the API-key path
// behaves identically and the invariant above still holds. It predates this
// change, by way of issue #118, so nothing here introduced it. And it is
// self-harming only: with the flag unset the request routes exactly as it does
// on main, so there is no narrowing, no cross-tenant effect and no billing
// effect, and the worst outcome is the caller's own turn reaching a route that
// answers 404 for tool use.
//
// The fix would be to fall through to the any-spelling scan below whenever the
// typed answer is empty. That is a second full decode of EVERY plain chat turn,
// which is the common path, spent to close a hole that only harms the sender.
// Pinned instead by fixtures in TestToolParamInBodyAgreesWithFirstToolParam, so
// the next reader finds a recorded answer rather than a surprise.
//
// ponytail: if a real caller ever trips this, the upgrade path is that
// fall-through, and the parse cost is the thing to measure first.
func ToolParamInBody(raw []byte) string {
	var req ChatCompletionRequest
	if err := json.Unmarshal(raw, &req); err == nil {
		return firstToolParam(&req)
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return ""
	}
	// Presence is collected first and the ordered list walked second, so the
	// reported name does not depend on map iteration order. A parameter spelled
	// two ways in one body counts as present, which is the gating direction.
	present := make(map[string]bool, len(fields))
	for key, value := range fields {
		if rawFieldPresent(value) {
			present[strings.ToLower(key)] = true
		}
	}
	for _, name := range toolParamNames {
		if present[name] {
			return name
		}
	}
	return ""
}

// firstToolParam returns the name of the first tool-calling or structured-output
// parameter present in the request, or "" if none are present.
//
// isPresent treats a json.RawMessage field as present when it is non-empty and
// not the JSON literal "null". This correctly handles `"tools": []` (empty
// array) which must be treated as present, not silently ignored.
func firstToolParam(req *ChatCompletionRequest) string {
	isPresent := func(f json.RawMessage) bool {
		return len(f) > 0 && string(f) != "null"
	}

	switch {
	case isPresent(req.Tools):
		return "tools"
	case isPresent(req.ToolChoice):
		return "tool_choice"
	case isPresent(req.ResponseFormat):
		return "response_format"
	case isPresent(req.Functions):
		return "functions"
	case isPresent(req.FunctionCall):
		return "function_call"
	}

	if req.ParallelToolCalls != nil {
		return "parallel_tool_calls"
	}

	return ""
}

// guardToolCapability probes the routing layer to determine whether the alias
// has at least one tool-capable route. If no capable route exists it writes a
// provider-blind 400 and returns true (caller must return). If a capable route
// exists (or the routing client is unavailable), it returns false so the request
// proceeds normally through the standard executeSync / executeStreaming path.
//
// The routing probe is a lightweight SelectRoute call with RequireToolCapable=true.
// Auth and billing are NOT performed here — the normal execution path handles them.
func guardToolCapability(ctx context.Context, o *Orchestrator, w http.ResponseWriter, model, param string) bool {
	if o.routing == nil {
		// No routing client (e.g. unit-test environment with bare Orchestrator).
		// Fail closed: reject the request as unsupported.
		writeUnsupportedParamError(w, param, model)
		return true
	}

	_, err := o.routing.SelectRoute(ctx, SelectRouteInput{
		AliasID:             model,
		NeedChatCompletions: true,
		RequireToolCapable:  true,
	})
	if err != nil {
		errMsg := err.Error()
		// 422 from the control-plane signals ErrNoCapableRoute: no tool-capable
		// route exists for this alias. Return a provider-blind 400.
		//
		// The sentinel is checked first and the substring match is kept behind
		// it: RoutingClient now wraps ErrNoToolCapableRoute, but o.routing is an
		// interface here and a test double still fabricates the raw string.
		//
		// The match is on the message, never on the status. A bare "422" also
		// matched ErrRouteNotEligible, which control-plane answers 422 for as
		// well, so an alias whose routes were all unhealthy or all filtered out
		// by an allowlist was told its response_format was unsupported: a claim
		// about the request when the truth was about route availability. It
		// also matched those three digits anywhere in a route or model id.
		if errors.Is(err, ErrNoToolCapableRoute) || strings.Contains(errMsg, "no tool-capable") {
			writeUnsupportedParamError(w, param, model)
			return true
		}
		// Any other routing failure (500, timeout, network error) is a transient
		// infrastructure problem, not a permanent capability mismatch. Return 502
		// so the caller knows to retry rather than treating it as a bad request.
		code := "routing_error"
		apierrors.WriteError(w, http.StatusBadGateway, "api_error",
			"Failed to verify tool-calling capability for this request.", &code)
		return true
	}

	// At least one capable route exists — let the request pass through.
	return false
}

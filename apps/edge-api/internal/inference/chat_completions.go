package inference

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	apierrors "github.com/sakibsadmanshajib/hive/apps/edge-api/internal/errors"
)

// handleChatCompletions handles POST /v1/chat/completions.
func handleChatCompletions(o *Orchestrator, w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 10*1024*1024))
	if err != nil {
		writeInvalidBodyError(w)
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
		o.executeStreaming(r.Context(), w, r, EndpointChatCompletions, body, req.Model, req.Model, needFlags, DefaultHoldText, includeUsage, req.ReasoningEffort, o.litellm.ChatCompletion)
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
		if strings.Contains(errMsg, "422") || strings.Contains(errMsg, "no tool-capable") {
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

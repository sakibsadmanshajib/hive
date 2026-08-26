package inference

import "github.com/google/uuid"

// mintCompletionID returns a gateway-owned response id in the given OpenAI
// id-family prefix ("chatcmpl" for chat completions, "cmpl" for legacy text
// completions), so an upstream's own id format never reaches the client.
//
// An upstream id format is provider-identifying by construction: OpenRouter
// mints "gen-*", Groq echoes back "chatcmpl-*" with its own internal suffix
// scheme, and any future provider carries its own shape again. That is
// exactly as much an invariant breach as a provider name inside an error
// string (CLAUDE.md: "provider names never leak to customers"), so every
// normalize boundary that builds a customer-facing response mints its own id
// here rather than forwarding the upstream's.
//
// Callers must mint once per response (or once per stream, reusing the same
// value for every chunk) and keep the original upstream id locally if they
// need it for log correlation -- see normalizeChatCompletion for the
// pattern. Internal request/billing correlation never depends on this
// value: that keys on attempt.ID, a separate id this gateway mints at
// dispatch time regardless of what any upstream returns.
func mintCompletionID(prefix string) string {
	return prefix + "-" + uuid.New().String()
}

// idPrefixForEndpoint returns the OpenAI id-family prefix matching an
// endpoint's non-streaming shape, so a streamed response's minted id looks
// like its synchronous twin (chatcmpl-* for chat completions, cmpl-* for
// legacy completions), per the id-stability requirement mintCompletionID
// documents.
func idPrefixForEndpoint(endpoint string) string {
	if endpoint == EndpointCompletions {
		return "cmpl"
	}
	return "chatcmpl"
}

// chunkFinished reports whether chunk carries a non-empty finish_reason on
// any choice.
func chunkFinished(chunk ChatCompletionChunk) bool {
	for _, choice := range chunk.Choices {
		if choice.FinishReason != nil && *choice.FinishReason != "" {
			return true
		}
	}
	return false
}

// shouldSuppressPostFinishChunk reports whether a streamed chunk must be
// dropped from the wire rather than relayed to the client, because a
// terminal finish_reason has already been relayed on an earlier chunk of
// this same response. The one exception is a genuine usage-only terminal
// frame delivered after finish_reason by design
// (stream_options.include_usage): that always forwards.
//
// Exists because DeepSeek-family streams via OpenRouter emit one extra
// empty role/content chunk after finish_reason=stop, before [DONE] -- a
// strict SSE client that already closed the message on the real finish
// frame chokes on anything more (parity finding, 2026-08-26).
func shouldSuppressPostFinishChunk(finishSeen bool, chunk ChatCompletionChunk) bool {
	return finishSeen && chunk.Usage == nil
}

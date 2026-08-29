package inference

import "github.com/sakibsadmanshajib/hive/packages/sanitize"

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
//
// Thin wrapper: the generator itself moved to packages/sanitize (issue
// #1235) so apps/control-plane's local batch executor can mint the exact
// same id shape without duplicating the one-line generator. Every call site
// in this package is unchanged.
func mintCompletionID(prefix string) string {
	return sanitize.MintID(prefix)
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

// ChunkFinished reports whether chunk carries a non-empty finish_reason on
// any choice. Exported so a second SSE relay outside this package (the RAG
// streaming path, apps/edge-api/internal/rag/chat_handler.go) can apply the
// same post-finish-chunk suppression rule as executeStreaming below, rather
// than duplicating this pure predicate.
func ChunkFinished(chunk ChatCompletionChunk) bool {
	for _, choice := range chunk.Choices {
		if choice.FinishReason != nil && *choice.FinishReason != "" {
			return true
		}
	}
	return false
}

// ShouldSuppressPostFinishChunk reports whether a streamed chunk must be
// dropped from the wire rather than relayed to the client, because a
// terminal finish_reason has already been relayed on an earlier chunk of
// this same response. The one exception is a genuine usage-only terminal
// frame delivered after finish_reason by design (stream_options.
// include_usage): that shape is usage set AND zero choices, matching a real
// terminal usage frame, and it always forwards. A chunk that carries usage
// alongside actual choice content is still suppressed -- usage presence
// alone is not the exception, an empty-choices usage-only shape is.
//
// Exists because DeepSeek-family streams via OpenRouter emit one extra
// empty role/content chunk after finish_reason=stop, before [DONE] -- a
// strict SSE client that already closed the message on the real finish
// frame chokes on anything more (parity finding, 2026-08-26). Exported for
// the same reason as ChunkFinished above.
func ShouldSuppressPostFinishChunk(finishSeen bool, chunk ChatCompletionChunk) bool {
	if !finishSeen {
		return false
	}
	isUsageOnlyTerminalFrame := chunk.Usage != nil && len(chunk.Choices) == 0
	return !isUsageOnlyTerminalFrame
}

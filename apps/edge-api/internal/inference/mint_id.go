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
// this same response. The one exception is the terminal usage frame
// stream_options.include_usage delivers after finish_reason by design: usage
// set AND nothing for the client to render. It always forwards, because it
// carries the only token count the caller will ever receive.
//
// "Nothing to render" is the load-bearing half, and it used to be spelled
// "zero choices". LiteLLM does not send that shape. Measured against the
// pinned v1.98.0 image and the free pool own upstream on 2026-08-28, its
// terminal usage frame is
//
//	{... "choices":[{"index":0,"delta":{}}],"usage":{"prompt_tokens":15,...}}
//
// one choice whose delta is entirely empty, not an empty choices array. The
// zero-choices test read that as an ordinary post-finish chunk and dropped it,
// so every streaming caller that asked for usage received none: the OpenAI
// surface silently lost its terminal frame, and the Anthropic surface, which
// folds this same relay, reported usage input_tokens 0 and output_tokens 0 on
// every /v1/messages response (issue #1329). Billing never noticed, because
// settlement reads the accumulator, which sees every frame before this
// decision is taken.
//
// A chunk that carries usage alongside real content, a refusal or a tool-call
// delta after finish is still suppressed: usage presence alone is not the
// exception, an empty-payload usage frame is.
//
// Exists because DeepSeek-family streams via OpenRouter emit one extra
// empty role/content chunk after finish_reason=stop, before [DONE] -- a
// strict SSE client that already closed the message on the real finish
// frame chokes on anything more (parity finding, 2026-08-26). That chunk
// carries no usage, so it is still dropped here. Exported for the same reason
// as ChunkFinished above: the RAG streaming relay applies the same rule.
func ShouldSuppressPostFinishChunk(finishSeen bool, chunk ChatCompletionChunk) bool {
	if !finishSeen {
		return false
	}
	isUsageOnlyTerminalFrame := chunk.Usage != nil && !chunkCarriesRenderablePayload(chunk)
	return !isUsageOnlyTerminalFrame
}

// chunkCarriesRenderablePayload reports whether any choice on the chunk carries
// something a client has to render: text content, a refusal, a tool call or a
// legacy function call. A choice whose delta is empty (or carries only a role)
// renders nothing, which is exactly the shape LiteLLM terminal usage frame
// takes.
func chunkCarriesRenderablePayload(chunk ChatCompletionChunk) bool {
	for _, choice := range chunk.Choices {
		if choice.Delta.Content != nil && *choice.Delta.Content != "" {
			return true
		}
		if choice.Delta.Refusal != nil && *choice.Delta.Refusal != "" {
			return true
		}
		if toolCallPresent(choice.Delta.ToolCalls) || toolCallPresent(choice.Delta.FunctionCall) {
			return true
		}
	}
	return false
}

package rag

// Money path for POST /v1/rag/chat (#669).
//
// Grounded chat used to serve inference and ask nothing: no balance check, no
// hold, no charge. It emitted a RAG_CHAT_COMPLETED audit event carrying the
// token counts and stopped there, so spend was reconstructable after the fact
// but never gated, and a tenant at zero credits kept getting answers. The
// handler's own comment said why: the reservation lifecycle in
// inference.Orchestrator resolves an "hk_..." API key out of the Authorization
// header, and this route is JWT-session only, so it could not call it.
//
// That reason expired when session chat got the same treatment in #746. The
// lifecycle now lives in internal/sessionbilling with no API-key dependency,
// and this file is the RAG-specific half: which endpoint, which hold floor,
// and how a completed generation turns into a charge.
//
// Nothing here invents pricing. The charge goes through the same two helpers
// the API-key path and session chat use, so all three carry one margin, one
// credit unit, one rounding rule and one never-free floor rather than three
// arithmetics that could disagree.

import (
	"encoding/json"
	"strings"

	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/inference"
)

// contentOf is the assistant text of a completed non-streaming response. It is
// the fallback quantity settlement prices when the upstream returned no usage
// block at all: a delivered answer settles at an estimate flagged unconfirmed,
// never at zero.
func contentOf(choices []ChatChoice) string {
	var b strings.Builder
	for _, c := range choices {
		b.WriteString(c.Message.Content)
	}
	return b.String()
}

// ragHoldCredits is the flat pre-dispatch hold a grounded chat turn takes: the
// same figure session chat and the API-key chat path reserve, because it is
// the same kind of request through the same models. A variable-price alias
// raises it per request; see sessionbilling.Start.
//
// It is an authorization floor, never a charge. Settlement releases it in full
// and posts the real metered cost.
const ragHoldCredits = inference.DefaultHoldText

// usageEnvelope reads the usage block off an upstream response (or a stream
// frame) in the two shapes the charge needs it: decoded, for token counts and
// the cache split, and raw, because an upstream_actual alias prices from the
// cost fields the upstream reported rather than from tokens.
type usageEnvelope struct {
	Usage    *inference.UsageResponse `json:"usage"`
	rawUsage []byte
}

// readUsage extracts the usage block from one upstream JSON document. A
// document with no usage block yields a zero envelope, which settles as
// unconfirmed rather than as a free request.
func readUsage(body []byte) usageEnvelope {
	var envelope struct {
		Usage json.RawMessage `json:"usage"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil || len(envelope.Usage) == 0 {
		return usageEnvelope{}
	}
	var decoded inference.UsageResponse
	if err := json.Unmarshal(envelope.Usage, &decoded); err != nil {
		return usageEnvelope{rawUsage: envelope.Usage}
	}
	return usageEnvelope{Usage: &decoded, rawUsage: envelope.Usage}
}

// settleChat prices one completed grounded chat.
//
// The two branches are the two pricing modes an alias can be in, and they are
// the same two session chat branches on:
//
//   - upstream_actual (hive-auto, per D-059): the catalog carries no token
//     price, so the charge is the cost the upstream reported for this
//     generation times the standard margin. A cost that is missing, unreadable
//     or a confident zero settles at the HOLD rather than at nothing, which is
//     the point: a delivered response is never free.
//   - fixed price: the charge is the alias's own catalog rate applied to the
//     tokens actually metered, split into fresh input, cache read and cache
//     write so each is priced at its own rate.
//
// delivered=false means nothing was produced, so there is no quantity to
// charge and the caller releases the hold instead.
func settleChat(route inference.SelectRouteResult, held int64, env usageEnvelope,
	alias, content string, requestBody []byte) (credits int64, confirmed, delivered bool) {

	hasUsage := env.Usage != nil
	var inTokens, outTokens int64
	var cache inference.CacheUsage
	if hasUsage {
		inTokens, outTokens = env.Usage.PromptTokens, env.Usage.CompletionTokens
		cache = inference.NormalizeCacheUsage(env.Usage, alias, route.Provider)
	}

	if route.Pricing.IsUpstreamActual() {
		settled := inference.UpstreamActualSettlement(env.rawUsage, held, hasUsage, inTokens, outTokens, content)
		return settled.Credits, settled.Confirmed, settled.Delivered
	}
	return inference.ChatSettlementCredits(route, hasUsage,
		cache.FreshInputTokens, cache.CacheReadTokens, cache.CacheWriteTokens, outTokens,
		requestBody, content)
}

// meteredTokens is what the settlement row records alongside the charge: the
// raw prompt and completion counts plus the cache components they split into.
func (e usageEnvelope) meteredTokens(alias, provider string) (in, out, cacheRead, cacheWrite int64) {
	if e.Usage == nil {
		return 0, 0, 0, 0
	}
	cache := inference.NormalizeCacheUsage(e.Usage, alias, provider)
	return e.Usage.PromptTokens, e.Usage.CompletionTokens, cache.CacheReadTokens, cache.CacheWriteTokens
}

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
	"log/slog"
	"strings"

	"github.com/google/uuid"
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
	Usage *inference.UsageResponse `json:"usage"`
	// rawDocument is the WHOLE upstream document, not the extracted usage
	// value. inference.ParseUpstreamCost decodes a frame with a top-level
	// "usage" key, so handing it the inner usage object finds no cost at all
	// and every upstream_actual request settles at the hold instead of at its
	// real cost: a sub-cent generation charged against a 0.10 USD floor, which
	// is the shape of issue #1198. The document also carries the generation id
	// at its top level, which the inner object loses.
	rawDocument []byte
}

// readUsage extracts the usage block from one upstream JSON document, keeping
// the document itself for the cost read. A document with no usage block still
// carries its bytes forward, so a missing usage block settles as unconfirmed
// rather than as a free request.
func readUsage(document []byte, alias, provider string) usageEnvelope {
	var envelope struct {
		Usage json.RawMessage `json:"usage"`
	}
	if err := json.Unmarshal(document, &envelope); err != nil {
		// A body that does not parse at all is already handled as a normalize
		// error by the sync caller, and a stream frame only reaches here after
		// the sanitizer accepted it. Log it anyway: reaching this line means
		// the charge is about to be derived from nothing, and silence is how a
		// systematic mispricing goes unnoticed.
		slog.Warn("rag chat: upstream document did not parse, charge falls back to an estimate",
			"err", err, "alias", alias, "provider", provider)
		return usageEnvelope{}
	}
	if len(envelope.Usage) == 0 {
		return usageEnvelope{rawDocument: document}
	}
	var decoded inference.UsageResponse
	if err := json.Unmarshal(envelope.Usage, &decoded); err != nil {
		// A usage block that is present but does not decode drops a fixed-price
		// charge from real metered tokens to a content-length estimate, for
		// every request, for as long as the shape is wrong. That is a revenue
		// drift with no other operator signal.
		slog.Warn("rag chat: upstream usage block did not decode, charge falls back to an estimate",
			"err", err, "alias", alias, "provider", provider)
		return usageEnvelope{rawDocument: document}
	}
	return usageEnvelope{Usage: &decoded, rawDocument: document}
}

// settlement is one priced turn: what to charge, whether the figure is measured
// truth, whether anything was delivered at all, and the two fields that make a
// variable-price charge auditable afterwards.
type settlement struct {
	Credits   int64
	Confirmed bool
	Delivered bool
	// GenerationID is the upstream's own handle for the generation this charge
	// paid for. It exists only on the upstream_actual path, and it is the only
	// thing that links a ledger row back to the thing it was billed for when a
	// customer disputes a charge. Operator logs only, never an audit event: an
	// upstream identifier can carry a provider name and audit_log fans out to
	// third-party sinks.
	GenerationID string
	// Reason is the pricing verdict ("upstream_cost" when the cost was read,
	// "upstream_cost_absent" and friends when it was not, "catalog_price" on
	// the fixed-price path). Discarding it hid the difference between a charge
	// derived from a reported cost and one that fell back to the hold, which is
	// exactly the distinction this endpoint got wrong.
	Reason string
	// ZeroContent narrows Delivered=false: the turn produced tokens the
	// customer could not read (issue #1526). It is what the caller records as
	// the release reason, so a reasoning burn is distinguishable in the ledger
	// from a provider that died or a customer who hung up on a blank answer.
	ZeroContent bool
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
// shape carries the delivery evidence the zero-content guard decides on, built
// by whichever half of this handler is settling: the streaming relay's own
// accumulator, or the choices of a fully decoded response body. It is only
// consulted on the fixed-price branch, because that is the branch that reaches
// ChatSettlementCredits.
func settleChat(route inference.SelectRouteResult, held int64, env usageEnvelope,
	alias, content string, requestBody []byte, shape inference.DeliveryShape) settlement {

	hasUsage := env.Usage != nil
	var inTokens, outTokens int64
	var cache inference.CacheUsage
	if hasUsage {
		inTokens, outTokens = env.Usage.PromptTokens, env.Usage.CompletionTokens
		cache = inference.NormalizeCacheUsage(env.Usage, alias, route.Provider)
	}

	if route.Pricing.IsUpstreamActual() {
		settled := inference.UpstreamActualSettlement(env.rawDocument, held, hasUsage, inTokens, outTokens, content)
		return settlement{
			Credits: settled.Credits, Confirmed: settled.Confirmed, Delivered: settled.Delivered,
			GenerationID: settled.GenerationID, Reason: settled.Reason,
		}
	}
	credits, confirmed, delivered, zeroContent := inference.ChatSettlementCredits(route, hasUsage,
		cache.FreshInputTokens, cache.CacheReadTokens, cache.CacheWriteTokens, outTokens,
		requestBody, content, shape)
	return settlement{
		Credits: credits, Confirmed: confirmed, Delivered: delivered,
		Reason: "catalog_price", ZeroContent: zeroContent,
	}
}

// syncDeliveryShape is the non-streaming half's delivery evidence (issue
// #1526).
//
// Completed is true unconditionally, and that is not a shortcut. On a stream
// the flag answers "did the upstream finish saying what it was going to say",
// which settlement cannot otherwise know; a response body that has already been
// read to the end and decoded answers it by construction, since both had to
// succeed before this line runs. Forcing the streaming test onto this path
// would instead make the guard permanently unreachable here, which is the
// silent-absence shape rather than a conservative one.
//
// HasToolCall stays false because this endpoint cannot produce one:
// dispatchBody carries model, messages and stream options and nothing else, so
// no tools field ever reaches the provider, and upstreamChatResponse
// accordingly does not decode tool_calls. If this handler ever forwards tools,
// that field and this line have to arrive together, or a tool call truncated at
// the ceiling would read as a reasoning burn and be served free.
func syncDeliveryShape(upstream upstreamChatResponse) inference.DeliveryShape {
	shape := inference.DeliveryShape{
		Surface:   inference.ZeroContentSurfaceRAGSync,
		Completed: true,
	}
	for _, choice := range upstream.Choices {
		if choice.FinishReason != nil {
			shape.ObserveFinishReason(*choice.FinishReason)
		}
	}
	return shape
}

// logSettlement records a variable-price charge with the upstream handle it was
// derived from, so a disputed charge can be traced to the generation it paid
// for. Operator log only, for the reason on settlement.GenerationID.
//
// The fixed-price path is not logged: its charge is reproducible from the
// catalog row and the token counts already on the settlement, so there is
// nothing here that the ledger row does not already carry.
func logSettlement(requestID uuid.UUID, alias string, held int64, s settlement) {
	if !s.Delivered || s.Reason == "catalog_price" {
		return
	}
	slog.Info("rag chat: variable-price settlement",
		"request_id", requestID, "alias", alias, "reason", s.Reason,
		"credits", s.Credits, "confirmed", s.Confirmed,
		"generation_id", s.GenerationID, "held_credits", held)
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

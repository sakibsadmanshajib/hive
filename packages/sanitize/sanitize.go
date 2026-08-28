// Package sanitize is Hive's single implementation of the provider-blindness
// invariant on customer-bound response bodies (CLAUDE.md: "provider names
// never leak to customers, sanitized at both the control-plane and edge
// boundaries"). It strips upstream-identifying fields -- an upstream's own
// response id format, system_fingerprint, the provider name, and
// provider-reported cost -- and rewrites id/model to gateway-owned values.
//
// Moved here from apps/edge-api/internal/inference (mint_id.go's
// mintCompletionID, upstream_cost.go's SanitizeVariablePriceFrame, PR #1222)
// so apps/control-plane's local batch executor (issue #1235) can reuse the
// exact same logic. Go's internal-package visibility rule blocks a direct
// cross-app import of apps/edge-api/internal/inference from control-plane
// (see apps/control-plane/internal/batchstore/local_inference.go's doc
// comment on why the two apps stay import-separated), so this package is the
// shared location both apps import instead. edge-api's mintCompletionID and
// SanitizeVariablePriceFrame are now thin wrappers delegating here -- every
// existing edge-api call site is unchanged. Two implementations of one
// sanitiser is how they drift; this package is the one copy.
package sanitize

import (
	"encoding/json"

	"github.com/google/uuid"
)

// MintID returns a gateway-owned response id in the given OpenAI id-family
// prefix ("chatcmpl" for chat completions, "cmpl" for legacy text
// completions, "batch_req" for batch line envelopes), so an upstream's own
// id format never reaches the client.
//
// An upstream id format is provider-identifying by construction: OpenRouter
// mints "gen-*", Groq echoes back "chatcmpl-*" with its own internal suffix
// scheme, and any future provider carries its own shape again. That is
// exactly as much an invariant breach as a provider name inside an error
// string, so every normalize boundary that builds a customer-facing response
// mints its own id here rather than forwarding the upstream's.
func MintID(prefix string) string {
	return prefix + "-" + uuid.New().String()
}

// VariablePriceFrame strips the fields an upstream adds that must never
// reach a customer (cost, provider identity), rewrites the model to the
// alias, and replaces the upstream id with the caller's mintedID.
//
// Runs unconditionally on every response frame, fixed-price included:
// cost-field stripping is a no-op on a frame that has none, so one path
// serves every pricing model rather than carrying a second, unsanitized one.
//
// mintedID must be the SAME value the caller mints once per response (or,
// for a stream, once per stream reused on every chunk), never a fresh id per
// call: this function sanitizes one frame at a time with no memory of any
// frame before it, so a caller that minted a fresh id here would break the
// id-stability contract.
//
// ok is false when the frame cannot be parsed. The caller must then DROP the
// frame rather than forward or store it, because an unparseable frame is
// exactly the one whose contents are unknown.
func VariablePriceFrame(payload []byte, aliasID, mintedID string) ([]byte, bool) {
	var frame map[string]json.RawMessage
	if err := json.Unmarshal(payload, &frame); err != nil {
		return nil, false
	}

	// Provider identity and our own cost. Deleting by key rather than
	// rebuilding from a typed struct keeps every field the caller
	// legitimately needs, including ones this package does not model, such
	// as tool calls.
	delete(frame, "provider")
	delete(frame, "system_fingerprint")

	// id is the same provider-identity leak MintID replaces: OpenRouter's
	// own "gen-*" id shape leaks upstream identity verbatim. Rewrite rather
	// than delete, using the caller's mintedID, so the frame never comes out
	// missing its id.
	if _, present := frame["id"]; present {
		id, err := json.Marshal(mintedID)
		if err != nil {
			return nil, false
		}
		frame["id"] = id
	}

	if rawUsage, present := frame["usage"]; present {
		var usage map[string]json.RawMessage
		if err := json.Unmarshal(rawUsage, &usage); err != nil {
			return nil, false
		}
		for _, key := range []string{"cost", "cost_details", "is_byok"} {
			delete(usage, key)
		}
		rebuilt, err := json.Marshal(usage)
		if err != nil {
			return nil, false
		}
		frame["usage"] = rebuilt
	}

	// The router's chosen model. LiteLLM's model_name (e.g.
	// "route-deepseek-v4-pro") or a raw provider-qualified model string
	// (e.g. "openrouter/deepseek/deepseek-v4-pro-0813") is Hive's internal
	// routing detail, not what the customer requested; rewrite to the
	// customer-facing alias so internal routing naming never reaches them
	// either.
	if _, present := frame["model"]; present {
		alias, err := json.Marshal(aliasID)
		if err != nil {
			return nil, false
		}
		frame["model"] = alias
	}

	out, err := json.Marshal(frame)
	if err != nil {
		return nil, false
	}
	return out, true
}

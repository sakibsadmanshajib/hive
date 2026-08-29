// Package sanitize is Hive's single implementation of the provider-blindness
// invariant on customer-bound response bodies (CLAUDE.md: "provider names
// never leak to customers, sanitized at both the control-plane and edge
// boundaries"). VariablePriceFrame keeps only an explicit top-level
// allowlist (id, object, created, model, choices, usage), rewrites
// id/model to gateway-owned values, strips upstream-reported cost and
// per-choice provider_specific_fields, and rejects a null or
// error-carrying frame outright rather than sanitizing it into a
// misleading empty success.
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
	"strings"

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

// topLevelAllowlist is every top-level key VariablePriceFrame accepts as a
// legitimate customer-facing chat-completion field (matches the fields
// apps/edge-api/internal/inference.ChatCompletionResponse declares, minus
// system_fingerprint, which is dropped everywhere). This is the fix for the
// shape complaint the earlier delete-by-name version had: that version was
// a denylist, exactly as wide as its literal delete list, so any future
// upstream or proxy field it did not already know about (a new top-level
// key this package has never seen) passed straight through unsanitized. An
// allowlist inverts the default: an unrecognized key is dropped, not kept,
// so the next unforeseen field fails to leak rather than fails to be
// caught by a test (issue #1253 review). choices[] elements are
// deliberately NOT similarly allowlisted here; that stays scoped to what
// has actually been observed there (provider_specific_fields, issue
// #1280), a narrower, separately-considered follow-up.
var topLevelAllowlist = map[string]bool{
	"id": true, "object": true, "created": true, "model": true,
	"choices": true, "usage": true,
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
// ok is false when the frame cannot be parsed, is JSON null, carries a
// top-level "error" object, or the parsed frame is otherwise not a usable
// success shape. The caller must then DROP the frame rather than forward
// or store it, because an unparseable or error-shaped frame is exactly the
// one whose contents cannot be trusted as a completed result.
func VariablePriceFrame(payload []byte, aliasID, mintedID string) ([]byte, bool) {
	var frame map[string]json.RawMessage
	if err := json.Unmarshal(payload, &frame); err != nil {
		return nil, false
	}
	// A JSON "null" body unmarshals to a nil map with no error. Left
	// unchecked, every "present" guard below is false, the allowlist loop
	// copies nothing, and json.Marshal(nil map) legally re-encodes to
	// "null" -- an empty, technically-valid frame the caller would then
	// store as a completed success (issue #1253 review, CodeRabbit).
	if frame == nil {
		return nil, false
	}
	// A 2xx status carrying a top-level "error" object is an upstream
	// failure delivered inside a success status -- observed live from
	// OpenRouter, whose error.metadata.provider_name/raw fields carry
	// upstream identity and the upstream's own error text straight
	// through. Nothing else in this function inspects "error", so without
	// this check that shape would sanitize cleanly and be stored as a
	// completed line (issue #1253 review).
	//
	// The test is on the VALUE, not on the key's presence. Several
	// OpenAI-compatible upstreams declare "error" on every chunk of a
	// perfectly healthy stream and leave it empty, and keying on presence
	// alone rejected all of those: a provider that streams
	// {"error":null,"choices":[{"delta":{"content":"hi"}}]} had every
	// content frame dropped by the SSE relays and every batch line refused
	// here, with no error shown to anyone (PR #1303 review). null, "", {},
	// [] and false all mean the same thing an absent key means.
	if raw, present := frame["error"]; present && !emptyJSONValue(raw) {
		return nil, false
	}

	// Drop every key not on the allowlist before doing anything else, so
	// every transformation below operates on an already-known-safe field
	// set.
	kept := make(map[string]json.RawMessage, len(frame))
	for key, val := range frame {
		if topLevelAllowlist[key] {
			kept[key] = val
		}
	}
	frame = kept

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

	// provider_specific_fields: OpenRouter's own wrapper-schema convention
	// for passing provider-specific extension data through the proxy layer
	// (observed live, 2026-08-28: {"native_finish_reason":"stop"} on the
	// choice, {"reasoning":null,"refusal":null} on choice.message). The key
	// name itself is a routing-layer identity signal independent of its
	// contents -- its mere presence tells a customer they're behind an
	// OpenRouter-style multi-provider router -- so it is stripped
	// unconditionally at both nesting depths it has been observed at,
	// rather than allowlisted by content (issue #1280).
	if rawChoices, present := frame["choices"]; present {
		var choices []map[string]json.RawMessage
		if err := json.Unmarshal(rawChoices, &choices); err != nil {
			return nil, false
		}
		for i, choice := range choices {
			delete(choice, "provider_specific_fields")
			if rawMessage, present := choice["message"]; present {
				var message map[string]json.RawMessage
				if err := json.Unmarshal(rawMessage, &message); err != nil {
					return nil, false
				}
				delete(message, "provider_specific_fields")
				rebuiltMessage, err := json.Marshal(message)
				if err != nil {
					return nil, false
				}
				choice["message"] = rebuiltMessage
			}
			choices[i] = choice
		}
		rebuiltChoices, err := json.Marshal(choices)
		if err != nil {
			return nil, false
		}
		frame["choices"] = rebuiltChoices
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

// emptyJSONValue reports whether raw carries no information: JSON null, an
// empty or blank string, an empty object, an empty array, or false. Used for
// the "error" field, which upstreams declare-and-zero rather than omit.
//
// Parsed rather than string-compared so whitespace, "{ }" and "\u0020" are
// all handled by encoding/json instead of by a literal list this package
// would have to keep complete.
func emptyJSONValue(raw json.RawMessage) bool {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return false
	}
	switch typed := value.(type) {
	case nil:
		return true
	case bool:
		return !typed
	case string:
		return strings.TrimSpace(typed) == ""
	case []any:
		return len(typed) == 0
	case map[string]any:
		return len(typed) == 0
	}
	return false
}

// ReplaceErrorFrame reports whether payload is a parseable frame carrying a
// non-empty top-level "error" and, if so, returns a gateway-owned replacement
// frame carrying message, plus the raw upstream error value for the operator
// log. Nothing upstream survives into the replacement, including keys this
// package does not model.
//
// Opt-in at the call site, deliberately NOT folded into VariablePriceFrame.
// That function's ok == false means "drop this frame", and
// apps/control-plane's batch dispatcher depends on it to refuse storing an
// error-carrying line as a completed 2xx output line and charging credits for
// it (batchstore/executor/dispatcher.go, issue #1235). An SSE relay wants the
// opposite: a customer whose stream dies mid-answer is better served by a
// rendered error than by a silent truncation, because the status was
// committed as 200 before the failure existed and there is no other way left
// to tell them. Two behaviours, two calls, rather than one changed default
// that would turn a refused batch line into a billed one (PR #1303 review).
//
// The frame is REPLACED, never scrubbed field by field: mid-stream there is
// no HTTP status to classify by, there is no finite list of ways a provider
// can phrase a refusal, and a scrub forwards whatever the next one writes.
func ReplaceErrorFrame(payload []byte, message string) (replacement []byte, upstream string, ok bool) {
	var frame map[string]json.RawMessage
	if err := json.Unmarshal(payload, &frame); err != nil || frame == nil {
		return nil, "", false
	}
	raw, present := frame["error"]
	if !present || emptyJSONValue(raw) {
		return nil, "", false
	}
	out, err := json.Marshal(map[string]any{
		"error": map[string]string{
			"message": message,
			"type":    "api_error",
			"code":    "upstream_error",
		},
	})
	if err != nil {
		return nil, "", false
	}
	return out, string(raw), true
}

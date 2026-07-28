// tools/lint-no-client-cost-fields.test.mjs
// Fixture tests for tools/lint-no-client-cost-fields.mjs, written before the
// lint module existed (TDD RED first). Dependency-free, node:assert only,
// matching the style every tools/lint-*.mjs script already uses for its own
// inline selfTest(). Kept as its own file because the task requires a
// separate test file rather than folding these cases into the lint script's
// existing selfTest() convention.
//
// Run: node tools/lint-no-client-cost-fields.test.mjs

import assert from "node:assert/strict";
import {
  findClientCostFieldViolations,
  isAllowlistedPath,
} from "./lint-no-client-cost-fields.mjs";

// A path the lint has no reason to treat as internal-only.
const CUSTOMER_PATH = "apps/edge-api/internal/chat/response.go";

// --- Case 1: a fixture that DOES expose a provider field in a customer-bound
// struct must be flagged. ---
{
  const offendingProvider = `
    package chat

    type ChatCompletionResponse struct {
      ID       string \`json:"id"\`
      Model    string \`json:"model"\`
      Provider string \`json:"provider"\`
    }
  `;
  const violations = findClientCostFieldViolations(CUSTOMER_PATH, offendingProvider);
  assert.equal(
    violations.length,
    1,
    "a customer-bound struct with a provider field must be flagged exactly once",
  );
  assert.equal(violations[0].tag, "provider");
}

// --- Case 2: a fixture that DOES expose a USD field in a customer-bound
// struct must be flagged (the exact regression PR #137 fixed). ---
{
  const offendingUsd = `
    package chat

    type ChatCompletionResponse struct {
      ID        string \`json:"id"\`
      AmountUsd float64 \`json:"amount_usd"\`
    }
  `;
  const violations = findClientCostFieldViolations(CUSTOMER_PATH, offendingUsd);
  assert.equal(violations.length, 1, "a customer-bound struct with amount_usd must be flagged");
  assert.equal(violations[0].tag, "amount_usd");
}

// --- Case 3: a clean fixture must not be flagged. Includes the customer-
// facing price_credits fields deliberately, since these are the fields a
// customer IS supposed to see (what they pay, in credits, no FX) and must
// never be confused with the forbidden cost/provider/USD fields (what Hive
// pays, or which upstream delivered it). This is the false-positive guard:
// a lint that cannot tell "price to customer" from "Hive's cost" would cry
// wolf on every catalog response and get disabled, same fate as the FX guard
// deleted 2026-07-19. ---
{
  const clean = `
    package chat

    type ChatCompletionResponse struct {
      ID                 string \`json:"id"\`
      Model              string \`json:"model"\`
      Usage              Usage  \`json:"usage"\`
    }

    type ModelPricing struct {
      InputPriceCredits  int64 \`json:"input_price_credits"\`
      OutputPriceCredits int64 \`json:"output_price_credits"\`
    }
  `;
  const violations = findClientCostFieldViolations(CUSTOMER_PATH, clean);
  assert.equal(violations.length, 0, "a clean customer-facing fixture must not be flagged");
}

// --- Case 4: an internal-only path (the shadow-mode verdict records this
// step's brief describes, section 3.3/4: metering_shadow_verdicts is a
// log-only table, never read by any customer-facing path) must not be
// flagged even though the same field names appear. ---
{
  const shadowVerdictLogger = `
    package metering

    // verdictRow mirrors public.metering_shadow_verdicts. Never marshaled to
    // an HTTP response; written once, post-response, for grading only.
    type verdictRow struct {
      RequestID                string \`json:"request_id"\`
      Provider                 string \`json:"provider"\`
      EstimatedCreditsPerModel int64  \`json:"estimated_credits_per_model"\`
      WouldRefuseCode          string \`json:"would_refuse_code,omitempty"\`
    }
  `;
  const shadowPath = "apps/edge-api/internal/metering/verdictlog.go";
  assert.equal(isAllowlistedPath(shadowPath), true, "the metering package must be allowlisted");
  const violations = findClientCostFieldViolations(shadowPath, shadowVerdictLogger);
  assert.equal(
    violations.length,
    0,
    "the internal-only shadow-mode verdict logger must not be flagged",
  );
}

// --- Case 5: the real internal-only shape that exists on main today
// (routing.SelectionResult / SelectRouteResult): a service-to-service
// payload between control-plane and edge-api over the internal
// /internal/routing/select endpoint, gated by a shared secret, never a
// customer response. Regression guard using the actual field shape at
// apps/control-plane/internal/routing/types.go:64 as of commit 573151d7. ---
{
  const routingSelectionResult = `
    package routing

    type SelectionResult struct {
      AliasID          string   \`json:"alias_id"\`
      RouteID          string   \`json:"route_id"\`
      LiteLLMModelName string   \`json:"litellm_model_name"\`
      Provider         string   \`json:"provider"\`
      FallbackRouteIDs []string \`json:"fallback_route_ids"\`
    }
  `;
  const routingPath = "apps/control-plane/internal/routing/types.go";
  assert.equal(isAllowlistedPath(routingPath), true, "the routing package must be allowlisted");
  const violations = findClientCostFieldViolations(routingPath, routingSelectionResult);
  assert.equal(violations.length, 0, "the internal routing service-to-service payload must not be flagged");
}

// --- Case 6 (false-positive guard): fields that merely CONTAIN "provider" or
// "cost" as a substring, with a different exact json tag, must not be
// flagged. These are real fields on main today (payments/types.go,
// usage/types.go) naming a payment-gateway provider id, not an LLM upstream
// provider identity -- a different concept the literal task wording ("cost
// or provider field") does not intend to catch. A substring match here would
// have made this lint fire on main from day one, which is exactly the
// "cries wolf, gets disabled" failure mode the brief warns about. ---
{
  const paymentProviderIds = `
    package payments

    type PaymentIntentResponse struct {
      ProviderIntentID string \`json:"provider_intent_id"\`
      ProviderEventID  string \`json:"provider_event_id"\`
    }
  `;
  const violations = findClientCostFieldViolations(CUSTOMER_PATH, paymentProviderIds);
  assert.equal(
    violations.length,
    0,
    "provider_intent_id/provider_event_id (payment-gateway ids) must not be confused with the LLM upstream provider field",
  );
}

console.log("lint-no-client-cost-fields.test: PASS");

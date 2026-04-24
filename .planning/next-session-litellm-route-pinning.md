# Next Session: Pin LiteLLM backing providers for `hive-default` to reduce variance

## Context

Staging burst on 2026-04-24 (`.planning/debug/flaky-usage-tokens-root-cause.md`)
captured wild variance for identical requests against
`api-hive.scubed.co/v1/chat/completions`, model `hive-default`:

| metric | min | max | observation |
|--------|----:|----:|-------------|
| `prompt_tokens`     | 4   | 70  | identical 3-word user message |
| `completion_tokens` | 0   | 442 | identical reply `ok` |

Cause: LiteLLM routes `hive-default` to multiple OpenRouter backing
providers with different tokenizers and different reasoning budgets. The
zero-ct billing leak (now fixed by #99 clamp) was the worst symptom but
the variance itself is still a customer-visible inconsistency:

- Unstable cost per request → unstable customer credit burn
- Reasoning-capable providers silently consume hidden tokens (the ct=442
  case has reasoning_tokens >> visible content)
- Latency variance — some upstreams are 10× slower than others

PR #99 fixes the *zero-ct billing leak*; this session attacks the
*upstream-variance root cause* so the clamp rarely needs to fire.

## Scope

`deploy/litellm/config.yaml` only. No edge-api change. No SDK change.

## Goal

For `hive-default`, define a deterministic, narrow provider preference
order so identical requests land on the same backing provider unless that
provider is unavailable. Acceptable outcomes:

- Same prompt → `prompt_tokens` within ±10% across 30 sequential requests
- Same prompt → `completion_tokens` distribution narrows; outliers bounded
- ct=0 events (post-clamp) become rare to none

## Step-by-step

1. Branch: `chore/litellm-pin-hive-default-providers`
2. Read `deploy/litellm/config.yaml`. Find the `hive-default` model entry.
   Note the current `model` field, fallback list, and any provider
   constraints.
3. Pull LiteLLM docs via Context7 (`resolve-library-id` → `query-docs`)
   for the OpenRouter `provider_order` / `provider.preferences` syntax.
   Confirm supported syntax in the version pinned in
   `deploy/docker/docker-compose.yml` (search `image: ghcr.io/berriai/litellm`
   or similar to get version).
4. Pick 1–2 backing providers that:
   - Report usage reliably (no zero-ct flakes in our 17-sample burst —
     identify by the `gen-*` IDs that DID return non-zero ct)
   - Have low pt variance (consistent system-prompt overhead)
   - Are not reasoning-only models (avoid the ct=442 path unless that
     reasoning is intentional for `hive-default`)
5. Add `provider_order: [<picked>]` (or equivalent) under the model entry.
   Optionally `allow_fallbacks: false` if we want strict pinning, OR
   `fallbacks: [<safer set>]` if we want graceful degrade.
6. Apply the same treatment for the `hive-fast` and `hive-auto` aliases
   if their behavior shows the same variance — verify with a separate
   small burst before changing.
7. Validate locally:
   ```
   cd deploy/docker && docker compose --env-file ../../.env up litellm
   curl -sS -H "Authorization: Bearer $LITELLM_MASTER_KEY" \
     -d '{"model":"hive-default","messages":[{"role":"user","content":"ping"}]}' \
     -H 'content-type: application/json' \
     http://localhost:4000/v1/chat/completions | jq '.usage, .id'
   ```
   Repeat 10×, confirm pt collapses to a narrow band.
8. PR title: `chore(litellm): pin hive-default backing providers`
9. PR body: include before/after pt+ct distribution from a 30-curl burst.

## Constraints

- Do NOT remove the model alias entirely — `hive-default` must continue to
  resolve.
- Do NOT touch `hive-embedding-default` — embed routes are separate, no
  variance issue observed.
- NEVER push directly to main — feature branch + PR.

## Why this is separate from PR #99

#99 is defensive (clamp ct=0 to a tokenizer estimate so the ledger never
records 0 on real output). This session is preventive (stop landing on
bad providers in the first place). The two are independent and can land
in either order.

## Files

- `deploy/litellm/config.yaml`

## Out of scope

- Adding new model aliases
- Changing pricing in `apps/control-plane` catalog
- Switching off OpenRouter to a direct provider key (separate negotiation)

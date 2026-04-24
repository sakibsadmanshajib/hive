# Next Session: Investigate flaky `usage.*_tokens = 0` from chat + responses

## Scope

Root-cause investigation, then fix. Not a cosmetic tweak — affects billing attribution if customers rely on reported usage.

## Observed

During staging replay (2026-04-24) against `api-hive.scubed.co`:

- `tests/responses/responses.test.ts > Responses API > returns a valid response via SDK` — failed: `expected 0 to be greater than 0` on `response.usage.output_tokens`
- `tests/test_chat_completions.py::test_chat_completion_basic` — failed: `usage.completion_tokens == 0`, `prompt_tokens=3`, `total_tokens=3`
- Re-run of same tests passed. Intermittent. Not a stable repro.

Sample failing response shape:
```
ChatCompletion(id='gen-1777054061-gUjqoLWQL25di7YSJp8O',
  usage=CompletionUsage(
    completion_tokens=0,
    prompt_tokens=3,
    total_tokens=3,
    completion_tokens_details=CompletionTokensDetails(...)
  )
)
```

`id` prefix `gen-*` suggests OpenRouter upstream — usage block returned with zero output despite a valid completion body.

## Hypotheses (in priority order)

1. **OpenRouter usage reporting delay/skew** — some provider backends return `usage: null` or `0` on streaming-initiated non-stream calls; LiteLLM propagates. Known issue in `litellm` + some OR routes.
2. **LiteLLM route-level `mode: chat` caching** collapses usage when cached response served.
3. **edge-api usage aggregation bug** — `apps/edge-api/internal/billing/...` or wherever usage is extracted may zero-out on provider-blind error sanitization path.
4. **Specific upstream model returning `usage: {}` for short outputs** — some OR endpoints skip usage for responses < N tokens.

## Investigation plan

1. Branch: `debug/flaky-usage-tokens`
2. Reproduce with instrumented curl — bypass SDK:
   ```bash
   for i in $(seq 1 50); do
     curl -sS -H "Authorization: Bearer $HIVE_API_KEY" \
       -X POST https://api-hive.scubed.co/v1/chat/completions \
       -H 'content-type: application/json' \
       -d '{"model":"hive-default","messages":[{"role":"user","content":"ping"}]}' \
       | jq -c '{id, usage}'
   done | tee /tmp/usage-samples.jsonl
   grep '"completion_tokens":0' /tmp/usage-samples.jsonl | wc -l
   ```
   Establish flake rate (target: <1% acceptable, >5% = systemic).
3. Run same via LiteLLM direct (bypass edge-api) to isolate edge vs upstream:
   ```bash
   curl -sS -H "Authorization: Bearer $LITELLM_MASTER_KEY" \
     http://<staging-vm-internal>:4000/v1/chat/completions \
     -d '{"model":"route-openrouter-default",...}'
   ```
4. If LiteLLM also returns 0 → upstream (OpenRouter) issue. Check OR dashboard / `litellm` GitHub issues for known usage bugs.
5. If LiteLLM clean but edge-api returns 0 → bug in `apps/edge-api/internal/...` usage extraction. Grep for `Usage` / `CompletionTokens` handling.
6. Once root-caused:
   - **Upstream bug** → pin affected OR provider out of route, or route-level `usage_fallback: true` in LiteLLM config if supported, or compute usage locally via tokenizer when upstream returns 0
   - **edge-api bug** → fix extraction in Go
7. Update tests to assert `>=0` temporarily **only if** a deterministic upstream issue is identified; otherwise keep strict `>0` and fix the source.

## Constraints

- Billing-critical — do not loosen tests without fixing root cause first
- Do not add `.retry(3)` to hide the flake
- If fix requires `apps/edge-api` change → write a Go unit test that reproduces the zero-usage path
- NEVER push directly to main

## Files likely involved

- `deploy/litellm/config.yaml` — route + fallback config
- `apps/edge-api/internal/billing/usage.go` (or similar — grep `CompletionTokens`)
- `apps/edge-api/internal/proxy/*.go` — response passthrough
- `packages/sdk-tests/js/tests/responses/responses.test.ts`
- `packages/sdk-tests/python/tests/test_chat_completions.py`

## Deliverable

1 investigation doc in `.planning/debug/flaky-usage-tokens-root-cause.md` + 1 fix PR once root cause confirmed.

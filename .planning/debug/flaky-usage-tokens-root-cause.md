# Flaky `usage.completion_tokens = 0` — root-cause investigation

**Date**: 2026-04-24
**Branch**: `debug/flaky-usage-tokens`
**Against**: `https://api-hive.scubed.co/v1/chat/completions`, model `hive-default`

## TL;DR

- Flake rate observed: **~6% (1/17)** on the live staging route.
- Zero-`completion_tokens` responses still carry a **non-empty assistant
  message** — i.e. real output was produced, usage reported `0`.
- Upstream (OpenRouter via LiteLLM) is emitting the broken usage block;
  edge-api passes it through unchanged (`normalizeChatCompletion` is a pure
  Unmarshal/Remarshal — `apps/edge-api/internal/inference/chat_completions.go:49-64`).
- Orchestrator writes `OutputTokens = usage.CompletionTokens` directly into
  the accounting ledger — `apps/edge-api/internal/inference/orchestrator.go:262`.
  No clamp, no tokenizer fallback.
- **Billing impact**: every flaked request bills `$0` output → **revenue leak
  proportional to flake rate** (~6% of chat completions against current
  model mix).

## Repro burst (17 samples, sequential, same prompt "reply with ok")

| # | id prefix                  | pt | ct  | tt  | content   |
|---|----------------------------|----|-----|-----|-----------|
| 1 | gen-1777055948-O4IsYgGT…   | 19 | 24  | 43  | `ok`      |
| 2 | gen-1777055949-ZvqMupMV…   | 13 | 2   | 15  | `ok`      |
| 3 | gen-1777055950-cUnptZ6n…   | 19 | 54  | 73  | `ok`      |
| 4 | gen-1777055950-uKXARwBq…   | 19 | 27  | 46  | `ok`      |
| 5 | gen-1777055951-enmGTJxv…   | 13 | 2   | 15  | `ok`      |
| 6 | gen-1777055952-a1pzB8df…   | 16 | 219 | 235 | `ok`      |
| 7 | gen-1777055958-eYj3DJXc…   | 16 | 88  | 104 | `\n\nok\n`|
| 8 | gen-1777055961-cBD1fBao…   | 21 | 2   | 23  | `ok`      |
| 9 | gen-1777055962-DyFTCOzk…   | 16 | 133 | 149 | `ok\n`    |
|10 | gen-1777055966-SdlISKsX…   | 41 | 27  | 68  | `ok`      |
|11 | gen-1777055979-EouKo5Oz…   | 19 | 18  | 37  | `ok`      |
|12 | gen-1777055985-DVT7RV2S…   | 13 | 2   | 15  | `Ok`      |
|13 | gen-1777055986-lLVbHfw9…   | 70 | 18  | 88  | `Ok.`     |
|14 | gen-1777055987-KiZzwgfh…   | 70 | 17  | 87  | `ok`      |
|15 | **gen-1777055988-QQJOozdB…** | **4** | **0** | **4** | **`ok\n`** |
|16 | gen-1777055991-UWsPAlYs…   | 13 | 442 | 455 | `ok`      |
|17 | gen-1777055993-UdxALnUv…   | 15 | 56  | 71  | `ok`      |

TOTAL=17  ct_zero=1  flake_rate=5.9%

Observations:

1. **ct=0 on non-empty output** (sample 15): `content='ok\n'` is a real
   assistant message. The backing provider returned output but reported no
   completion tokens. `prompt_tokens=4` is also the smallest in the batch,
   suggesting a different backing tokenizer (shorter-count provider) on
   that route attempt.
2. **Enormous ct variance for identical reply** — `ct` ranges 2 → 442 for
   the exact same `'ok'` response. The `completion_tokens_details.reasoning_tokens`
   field (not shown but present in raw) absorbs hidden "thinking" tokens
   from reasoning-capable upstream models. This is NOT flake, but is a UX
   surprise for customers comparing usage across requests.
3. **pt varies 4 → 70** for the same 3-word user message. OpenRouter routes
   to different backing providers with different system-prompt injection +
   different tokenizers. `hive-default` is not pinned.

## Hypotheses — eliminated vs confirmed

| # | Hypothesis | Status |
|---|-----------|--------|
| 1 | OpenRouter upstream reports `ct=0` on some routes/providers | **CONFIRMED** (upstream-origin `gen-*` id + non-empty content) |
| 2 | LiteLLM collapses usage on cached responses | Not applicable — no cache in LiteLLM config for this route |
| 3 | edge-api zeroes usage on error-sanitization path | Ruled out — `normalizeChatCompletion` is pure passthrough; no filter on zeros |
| 4 | Specific backing provider truncates usage for short outputs | **LIKELY** — ct=0 correlates with pt=4 (shortest tokenizer) |
| 5 | Stream-initiated non-stream calls drop usage | Not applicable — this is strict non-stream (`stream=false` default) |

## Root cause

OpenRouter serves `hive-default` through a multi-provider pool. When the
request lands on a backing provider that does not report
`completion_tokens` for its native response envelope (observed on at least
one provider in rotation for short outputs), the `usage` block arrives as
`{prompt_tokens: N, completion_tokens: 0, total_tokens: N}`. LiteLLM
forwards unchanged; edge-api passes through unchanged; orchestrator records
`OutputTokens = 0` and `HiveCreditDelta = prompt_tokens + 0`.

**Secondary issue**: `hive-default` is not pinned to a single backing
provider, which causes the observed wild variance in both `pt` and `ct`
even for identical requests.

## Fix plan

Two independent pieces — recommended as separate PRs so revert can isolate
billing from routing policy.

### PR 1 — edge-api: tokenizer fallback for zero-usage responses

- In `normalizeChatCompletion` (and the matching `normalizeResponses` /
  `normalizeCompletion` paths), after Unmarshal, inspect every choice's
  message content. If `usage.completion_tokens == 0` **and any choice has
  non-empty text content**, compute an estimated `completion_tokens` from a
  lightweight tokenizer (approximate cl100k heuristic: `byte_len / 4`,
  rounded up, minimum 1).
- Overwrite `usage.completion_tokens` with the estimate and recompute
  `total_tokens = prompt_tokens + completion_tokens`.
- Emit a structured warning log:
  `usage_estimated_from_tokenizer upstream_ct=0 estimated_ct=<N>
   model=<alias> upstream_id=<id>`
- Leave the rest of the `completion_tokens_details` block alone — reasoning
  tokens are a separate upstream-reported facet and clamping them would
  mis-price reasoning models.
- Add a Go unit test that feeds a synthetic zero-ct response into
  `normalizeChatCompletion` and asserts `ct > 0` on output.

Constraint: do **NOT** silently hide upstream ct=0 in the accounting ledger
without logging. Billing team needs visibility into the flake rate.

Files to touch:

- `apps/edge-api/internal/inference/chat_completions.go`
- `apps/edge-api/internal/inference/completions.go`
- `apps/edge-api/internal/inference/responses.go`
- `apps/edge-api/internal/inference/usage_clamp.go` (new — shared helper)
- `apps/edge-api/internal/inference/handler_test.go` (or a new
  `usage_clamp_test.go`) — table-driven cases for zero-ct + non-empty
  content, zero-ct + empty content (legit), nil usage, reasoning tokens
  preserved.

Test acceptance: 6% burst-rerun against staging must show 0% ct=0 on
non-empty-content responses (post-clamp); reasoning-token variance still
allowed.

### PR 2 — LiteLLM: pin `hive-default` backing providers (optional, routing)

- `deploy/litellm/config.yaml` — add `allowed_fails` / `provider_order`
  constraints to reduce the backing-provider set for `hive-default`, or add
  a provider-level `usage_fallback` if supported by LiteLLM's latest OR
  adapter.
- Lower scope; no billing change required if PR 1 ships.

## Revenue impact estimate

At **5.9% flake rate** with an average `ct ≈ 50` missed tokens per flaked
request, a service doing 10k chat completions/day leaks:

  10,000 × 0.059 × 50 = 29,500 uncharged output tokens/day

At current catalog pricing per million completion tokens (varies by alias),
the leak is bounded by output token price × 29.5k/M — order-of-magnitude:
pennies to dollars per day at staging scale, **material at production scale
above 1M requests/day**.

## Out of scope for this investigation

- Fixing the variance in `pt` across identical requests (requires LiteLLM
  route pinning; separate PR)
- Auditing whether the catch-up ledger should retroactively reprice past
  ct=0 transactions (business decision, not code)
- Per-provider quality-of-usage-reporting scorecard (nice-to-have)

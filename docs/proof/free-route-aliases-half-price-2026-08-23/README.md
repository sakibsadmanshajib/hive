# hive-default and hive-auto: free OpenRouter upstream at half price

Captured 2026-08-23 on the branch `feat/free-route-aliases-half-price`.

Every credential is a placeholder in this log. The project's real
`OPENROUTER_API_KEY` was read from the shared `.env` at runtime and never
printed; the probe script logged only its length and a seven-character prefix,
neither of which is reproduced here. No URL in this capture carries a credential
in a query string.

## 1. The money proof: same request shapes, before and after

Produced by calling the production settlement function
`inference.CreditsForTokens` (`apps/edge-api/internal/inference/pricing.go`)
directly, once with the old catalog rates and once with the new ones, inside the
repo's own toolchain container. The arithmetic is not reimplemented here: that
function is the one the streaming and sync settlement paths both call, and it
delegates to `metering.ChargeCredits`, the single implementation of the
credits-per-million rule (D-031).

Two figures per shape, because they answer different questions.

- **exact (credit-millionths)** is quantity times rate, summed, before the single
  division and the round half up. This is where "exactly half" is provable.
- **settled credits** is the whole-credit charge the ledger records. Both sides
  round half up independently, so this can sit up to one credit above exactly
  half without any rate being wrong.

Old rates: hive-default 10500 in / 42000 out, hive-auto 21000 in / 84000 out.
New rates: hive-default 5250 / 21000, hive-auto 10500 / 42000.

| alias | request shape | prompt tok | completion tok | old exact (credit-millionths) | new exact | exact ratio | old settled credits | new settled credits |
|---|---|---|---|---|---|---|---|---|
| hive-default | typical chat turn | 1200 | 400 | 29400000 | 14700000 | 0.500000 | 29 | 15 |
| hive-default | long context read | 32000 | 800 | 369600000 | 184800000 | 0.500000 | 370 | 185 |
| hive-default | prompt only, no completion | 1500 | 0 | 15750000 | 7875000 | 0.500000 | 16 | 8 |
| hive-default | completion only, no prompt | 0 | 900 | 37800000 | 18900000 | 0.500000 | 38 | 19 |
| hive-default | tool call only turn, tiny completion | 850 | 40 | 10605000 | 5302500 | 0.500000 | 11 | 5 |
| hive-default | fail closed byte estimate (72 prompt, 1000 completion) | 72 | 1000 | 42756000 | 21378000 | 0.500000 | 43 | 21 |
| hive-default | one token each, floor territory | 1 | 1 | 52500 | 26250 | 0.500000 | 1 | 1 |
| hive-default | 24 completion tokens, floor boundary | 0 | 24 | 1008000 | 504000 | 0.500000 | 1 | 1 |
| hive-default | negative counts clamped | -5 | -5 | 0 | 0 | n/a | 0 | 0 |
| hive-auto | typical chat turn | 1200 | 400 | 58800000 | 29400000 | 0.500000 | 59 | 29 |
| hive-auto | long context read | 32000 | 800 | 739200000 | 369600000 | 0.500000 | 739 | 370 |
| hive-auto | prompt only, no completion | 1500 | 0 | 31500000 | 15750000 | 0.500000 | 32 | 16 |
| hive-auto | completion only, no prompt | 0 | 900 | 75600000 | 37800000 | 0.500000 | 76 | 38 |
| hive-auto | tool call only turn, tiny completion | 850 | 40 | 21210000 | 10605000 | 0.500000 | 21 | 11 |
| hive-auto | fail closed byte estimate (72 prompt, 1000 completion) | 72 | 1000 | 85512000 | 42756000 | 0.500000 | 86 | 43 |
| hive-auto | one token each, floor territory | 1 | 1 | 105000 | 52500 | 0.500000 | 1 | 1 |
| hive-auto | 24 completion tokens, floor boundary | 0 | 24 | 2016000 | 1008000 | 0.500000 | 2 | 1 |
| hive-auto | negative counts clamped | -5 | -5 | 0 | 0 | n/a | 0 | 0 |

Read the three columns that matter:

1. **Exact ratio is 0.500000 on every row.** Not "roughly half" and not "a
   fraction of a percent": exactly half, in integer rational arithmetic, for
   every shape including prompt-only, completion-only, the tool-call-only turn
   and the byte-estimated fail-closed shape.
2. **No row settles at zero where the old rate charged something.** The two rows
   that settle 0 are the negative-count shape, where both sides settle 0 because
   the counts are clamped to zero and nothing was billed before either.
3. **No row is billed more than half, beyond one credit of rounding.** The three
   rows where `new * 2` exceeds `old` do so by exactly one credit
   (15/29, 185/370 is exact, 370/739, 11/21, 1/1), which is the two independent
   half-up roundings and the pre-existing one-credit floor, not a rate error. One
   credit is 0.00001 USD at the repo's 100000-credits-per-USD constant.

The one-credit floor (`CreditsForTokens`) is what keeps a sub-credit request off
zero, and it is why the two "floor territory" rows read 1 and 1 rather than 1 and
0. That floor predates this change.

The assertions above were enforced, not merely eyeballed: the replay failed the
run if any exact ratio was not exactly one half, if any new charge was zero where
the old was positive, or if any new charge exceeded half by more than one credit.

## 2. Mutation testing of the new guards

`apps/control-plane/internal/routing/free_alias_pricing_test.go`. Six mutations
applied to the migration, each run through
`go test ./apps/control-plane/internal/routing/... -run 'Free|Retired'`:

| mutation | result |
|---|---|
| M1 revert hive-default input to the old 10500 | FAIL TestFreeAliasPricesAreExactlyHalfTheOldRates |
| M2 hive-default output 21000 becomes hive-auto's 10500 | FAIL TestFreeAliasPricesAreExactlyHalfTheOldRates |
| M3 route-free-auto loses supports_batch and both image flags | FAIL TestFreeRouteAutoCarriesTheSoleCapabilityFlagsForward |
| M4 provider_model loses the `:free` variant suffix | FAIL TestFreeAliasRoutesTargetAFreeOpenRouterModel |
| M5 route-free-default loses tools_supported | FAIL TestFreeRoutesKeepTheCapabilitiesTheirAliasesServeToday |
| M6 the retired Groq routes are left enabled | FAIL TestRetiredGroqRoutesAreDisabledAndRepointed |
| control, migration restored | ok |

Six of six mutations killed a guard. M1 is the specific one the brief asked for:
the suite goes red on the old rate and green on the new one.

## 3. The free target: live evidence, not documentation reading

`https://openrouter.ai/api/v1/models`, fetched 2026-08-23: 422 models, of which
22 price both prompt and completion at zero. A literal `openrouter/free` id does
exist; it is a router rather than a model.

Capability requirement, derived from what the two aliases serve today: both
current routes declare `tools_supported = true`, which is the column PR #206
routes `tools`, `tool_choice` and `response_format` on. Of the 22 free models,
five support all of tools, tool_choice, response_format and structured_outputs.
Joined to `https://openrouter.ai/api/frontend/v1/all-providers`:

| free model | provider | trains on prompts | retention |
|---|---|---|---|
| dots-studio/dots-3-note-preview:free | AtlasCloud | no | retains, period not published |
| z-ai/glm-5.2:free | Decart | no | zero retention |
| nvidia/nemotron-3-super-120b-a12b:free | NVIDIA | YES | retains |
| nvidia/nemotron-nano-9b-v2:free | NVIDIA | YES | retains |
| liquid/lfm-2.5-2.6b:free | Liquid | YES | retains |

A false-green worth recording, because the first version of this scan shipped it:
the endpoints API reports NVIDIA's provider as `Nvidia` while the provider
directory keys it under displayName `NVIDIA`. Joining on displayName alone misses
the record entirely and reports every NVIDIA free endpoint as no-training and
zero-retention, the exact opposite of the truth. Join on both fields.

### Live probe results

Requests sent straight to `https://openrouter.ai/api/v1/chat/completions` with
the project's key, five shots per configuration.

**`z-ai/glm-5.2:free`, the only zero-retention candidate: unusable.**

```
 sync status 429
  {"error":{"message":"Provider returned error","code":429,"metadata":{
     "raw":"z-ai/glm-5.2:free is temporarily rate-limited upstream. ...",
     "provider_name":"Decart","is_byok":false,
     "provider_error_code":"upstream_429",
     "limit_source":"upstream_provider_shared_pool", ...
 tools status 429            (same body)
 response_format status 429  (same body)
 stream status 429           (same body)
```

Four of four attempts refused. `limit_source` is an upstream provider shared
pool, so this is not our account's limit and buying credits cannot raise it.

**`openrouter/free` with no provider preference: rejected on output quality.**

```
 200 nvidia/nemotron-3-ultra-550b-a55b:free via Nvidia            'HIVE-OK'
 200 nvidia/nemotron-3.5-content-safety:free via Nvidia           'User Safety: safe'
 200 nvidia/nemotron-3.5-content-safety:free via Nvidia           ''
 200 nvidia/nemotron-3-nano-omni-30b-a3b-reasoning:free via Nvidia 'HIVE-OK'
 200 cohere/north-mini-code:free via Cohere                       'HIVE-OK'
 providers: {'Nvidia': 4, 'Cohere': 1}
```

Four of five landed on a provider that trains on prompts, and two of five landed
on a moderation classifier that answered a plain chat prompt with
`User Safety: safe` and then with an empty string.

**`openrouter/free` with `provider: {data_collection: "deny"}`: works, and the
filter is real.**

```
 200 cohere/north-mini-code:free via Cohere                       'HIVE-OK'
 200 dots-studio/dots-3-note-preview:free via AtlasCloud          'HIVE-OK'
 200 dots-studio/dots-3-note-preview:free via AtlasCloud          'HIVE-OK'
 200 cohere/north-mini-code:free via Cohere                       'HIVE-OK'
 200 dots-studio/dots-3-note-preview:free via AtlasCloud          'HIVE-OK'
 providers: {'Cohere': 2, 'AtlasCloud': 3}

 with tools:
 200 cohere/north-mini-code:free      tool_call=True
 200 dots-studio/dots-3-note-preview:free tool_call=True
 200 cohere/north-mini-code:free      tool_call=True
 200 cohere/north-mini-code:free      tool_call=True
 200 cohere/north-mini-code:free      tool_call=True
```

Every NVIDIA endpoint and the classifier are gone from the pool. Tool calls
produced on five of five.

**`provider: {zdr: true}`, the stricter form: nothing to route to.**

```
 404 {"error":{"message":"No endpoints found matching your data policy
      (Zero data retention). Configure: https://openrouter.ai/settings/privacy",
      "code":404}}   x3
```

There is no zero-data-retention free endpoint reachable at all today.

**`dots-studio/dots-3-note-preview:free`, the chosen target: full parity.**

```
 sync status 200
  served_model = dots-studio/dots-3-note-preview:free
  provider     = AtlasCloud
  usage = {"prompt_tokens": 19, "completion_tokens": 32, "total_tokens": 51,
           "cost": 0, "cost_details": {"upstream_inference_cost": 0}, ...}
 tools status 200
  served_model = dots-studio/dots-3-note-preview:free
  tool_calls   = [{"type":"function","function":{"name":"get_weather",
                   "arguments":"{\"city\": \"Dhaka\"}"}}]
  finish_reason = tool_calls
 response_format status 200
  text = '{"ok": true}'
 stream status 200 chunks 20
  final served_model = dots-studio/dots-3-note-preview:free
  usage = {"prompt_tokens": 16, "completion_tokens": 48, "total_tokens": 64, "cost": 0, ...}
```

`cost: 0` on every response is the upstream confirming the endpoint is free.

One behaviour to know rather than discover: this model enables reasoning by
default, and a small `max_tokens` is consumed by reasoning tokens before any
visible content appears (`completion_tokens: 32` with `reasoning_tokens: 34` and
an empty string back). At a realistic completion budget it answers normally, as
the `HIVE-OK` rows above show.

### Rate limits

From `https://openrouter.ai/docs/api_reference/limits.md`, whose MDX constants
resolve to: free model variants are capped at **20 requests per minute**, and at
**50 requests per day** below 10 dollars of lifetime credit purchases or **1000
per day** at or above it. `GET /api/v1/key` reports `is_free_tier: false` for
this account, and project memory records a 10 dollar purchase, so 1000 per day is
the expected tier. The key endpoint does not expose the purchased total, so that
is an expectation and not a measurement; its `rate_limit` field is documented as
deprecated and returns `requests: -1`.

Separately, and more sharply, the Decart 429 above shows a per-provider shared
pool can refuse every request regardless of our account's standing.

### What a customer sees at the cap, today

A roughly 60 second wait and then a **502** whose message is `context canceled`.
That is issue #1089, unchanged by this work: `num_retries: 3` with
`request_timeout: 45` retries a rate-limited deployment three times, the SDK
suites time out at 60 seconds, and the provider's own 429 with its retry hint
never leaves the LiteLLM container log. This change makes that path materially
more likely, because 20 requests per minute is a much tighter ceiling than Groq's.

Not fixed here, deliberately. The candidate fix
(`router_settings.retry_policy.RateLimitErrorRetries: 0`) lives in the
`litellm_settings` block that the config sync preserves verbatim, so a file edit
is inert on a live box and cannot be verified from a developer machine. #1089
reaches the same conclusion about itself.

### Logging and training terms

- **OpenRouter itself** stores no prompts or completions unless the account opts
  in. Both opt-ins (private input/output logging, and letting OpenRouter use
  inputs/outputs for a 1 percent discount) are off by default
  (`https://openrouter.ai/docs/guides/privacy/data-collection.md`). Request
  metadata (token counts, latency) is always retained.
- **AtlasCloud**, the provider serving the chosen model: `training: false`,
  `retainsPrompts: true`, with no retention period published.
- The route carries `provider.data_collection: deny`, which restricts routing to
  providers that do not collect user data, as a fail-closed guard against the
  policy changing or a second provider appearing.
- **The contradiction, stated because this product sells data sovereignty:** a
  strict zero-retention posture is not achievable on any free OpenRouter endpoint
  today, proved by the `zdr: true` 404 above. The best available free posture is
  a provider that does not train but does retain for an unpublished period. The
  owner directed the move to free models with this on the record.

## 4. What is NOT proved here

- **That the running gateway serves the new route.** The LiteLLM config is
  seeded into a named volume only when absent, and the live change arrives
  through `POST /internal/litellm/sync` reading `provider_routes`. There is no
  SSH to the demo box from this environment and CI is the only remote hands, so
  the on-box confirmation belongs to the deploy run. `deploy-demo-box.yml`'s
  "Assert model catalog prices agree with the model LiteLLM will call" step is
  the check that catches a stale volume: it compares
  `provider_routes.provider_model` against what the live gateway resolves, and
  its predicate (`pricing_mode = 'upstream_actual' OR input_price_credits > 0`)
  covers both of these rows.
- **A ledger row at the new price.** That needs a served request against the
  deployed stack after the migration applies. What is proved above is the
  arithmetic, through the production function, for every request shape the alias
  bills.
- **No screenshot.** This change alters no UI surface. The console catalog table
  renders whatever price the API returns, and the two figures it will show are
  the ones proved in section 1.

## 5. Commands

```
# money replay, through the production settlement function
cd deploy/docker && docker compose --profile tools run --rm toolchain \
  "cd /workspace && go test ./apps/edge-api/internal/inference/... -count=1 -short -run TestZZHalvingReplay -v"
# (temporary harness, deleted after capture; not part of the branch)

# new offline guards
cd deploy/docker && docker compose --profile tools run --rm toolchain \
  "cd /workspace && go test ./apps/control-plane/internal/routing/... -count=1 -short -run 'Free|Retired' -v"

# full suites
cd deploy/docker && docker compose --profile tools run --rm toolchain \
  "cd /workspace && go test ./apps/control-plane/... -count=1 -short"
cd deploy/docker && docker compose --profile tools run --rm toolchain \
  "cd /workspace && go test ./apps/edge-api/... -count=1 -short"

# config lints
npm run lint:litellm-config     # PASS
npm run lint:litellm-routing    # PASS

# live probes (key read from .env at runtime, never printed)
curl -s https://openrouter.ai/api/v1/models
curl -s https://openrouter.ai/api/frontend/v1/all-providers
curl -s -H "Authorization: Bearer <OPENROUTER_API_KEY>" \
  -d '{"model":"dots-studio/dots-3-note-preview:free","messages":[...]}' \
  https://openrouter.ai/api/v1/chat/completions
```

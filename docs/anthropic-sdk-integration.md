# Pointing a real Anthropic SDK at Hive

This is the exact, tested path for using an unmodified Anthropic SDK client
(or an Anthropic-Messages-compatible agent CLI) against Hive by changing only
the base URL and the API key. Every claim below was verified against the live
deployed gateway (`https://api-hive.scubed.co`) with a fresh key minted
through the real developer console (`console-hive.scubed.co`), using the
real, installed `anthropic` SDK (Python 0.122.0), on 2026-08-18, after PR
#954's fix and this document's own fix were both deployed. See
`apps/edge-api/internal/anthropic/` for the implementation and
`apps/edge-api/internal/anthropic/*_test.go` for the test suite this doc is
backed by.

## Base URL

```
https://api-hive.scubed.co
```

No `/v1` suffix. The Anthropic SDK's default `base_url` convention
(`https://api.anthropic.com`) carries no path suffix; the SDK appends
`/v1/messages` itself. This is the opposite convention from the
OpenAI-compatible surface, whose base URL in this repo already includes `/v1`
(`https://api-hive.scubed.co/v1`) — getting this backwards is the most common
way to break an otherwise-correct integration.

## Auth header

`x-api-key: <API_KEY>` — the SDK's default when constructed with `api_key=`.
Hive also accepts `Authorization: Bearer <API_KEY>` (the `auth_token=`
constructor argument in the Python/TS SDKs), which reaches the same code path.
The key itself is a Hive API key (`hk_...`), minted from the developer console
(`https://console-hive.scubed.co/console/api-keys`), not an Anthropic key —
Hive never talks to Anthropic itself; it translates the wire protocol and
dispatches to whichever provider the requested alias routes to.

**Confirmed live 2026-08-18**: an `x-api-key` request against the deployed
gateway with a key minted through the actual console `POST
/api/v1/accounts/current/api-keys` route no longer 401s. A bogus key correctly
answers `401 authentication_error` in the real Anthropic error shape
(`{"type":"error","error":{"type":"authentication_error", "code":
"invalid_api_key", ...}}`), and a valid key reaches the model.

## Python

```python
from anthropic import Anthropic

client = Anthropic(
    base_url="https://api-hive.scubed.co",
    api_key="<API_KEY>",
)

message = client.messages.create(
    model="hive-default",
    max_tokens=256,
    messages=[{"role": "user", "content": "Hello"}],
)
print(message.content[0].text)
```

Streaming:

```python
with client.messages.stream(
    model="hive-default",
    max_tokens=256,
    messages=[{"role": "user", "content": "Hello"}],
) as stream:
    for text in stream.text_stream:
        print(text, end="", flush=True)
```

## TypeScript

```ts
import Anthropic from "@anthropic-ai/sdk";

const client = new Anthropic({
  baseURL: "https://api-hive.scubed.co",
  apiKey: "<API_KEY>",
});

const message = await client.messages.create({
  model: "hive-default",
  max_tokens: 256,
  messages: [{ role: "user", content: "Hello" }],
});
```

## Model aliases that actually work on /v1/messages

Live from `GET https://api-hive.scubed.co/catalog/models` (no auth required,
provider-blind by design — it never names the upstream provider):

| Alias | What it is | Verified live 2026-08-18 |
| --- | --- | --- |
| `hive-default` | Balanced default, stable | Non-streaming, streaming, and tool-use all confirmed with the real SDK. Use this as the example model. |
| `hive-auto` | Preview, widens internally when needed | Non-streaming confirmed. |
| `hive-fast` | Low-latency, stable | **Currently broken — do not use as an example model.** See "Known limitations" below. |

`hive-embedding-default`, `hive-stt`, `hive-tts` also appear in the catalog but
are not chat models and do not answer `/v1/messages`. An alias not in this
list, or a real upstream route id (e.g. anything starting `route-`), is
refused with a provider-blind 404 before any upstream dispatch happens — this
is enforced behavior, not a bug (see `TestMessages_RawLiteLLMRouteIDIsRefused
AndNeverDispatched`).

## Agent CLI config (OpenCode / Claude-Code-compatible clients)

**The base URL convention here is not the same as the raw SDK's above.**
OpenCode's Anthropic provider is `@ai-sdk/anthropic` (Vercel AI SDK), and it
does not append `/v1` itself the way the official `anthropic` package does
-- it appends only `/messages` to whatever `baseURL` you give it. Confirmed
live 2026-08-18: pointing OpenCode's config at
`https://api-hive.scubed.co` (no `/v1`, matching the raw-SDK convention
above) produced a real `404` at `https://api-hive.scubed.co/messages`. The
`baseURL` below therefore carries `/v1` explicitly, unlike the Python/TS
examples above it.

```json
{
  "provider": {
    "hive": {
      "npm": "@ai-sdk/anthropic",
      "options": {
        "baseURL": "https://api-hive.scubed.co/v1",
        "apiKey": "<API_KEY>"
      },
      "models": {
        "hive-default": {},
        "hive-auto": {}
      }
    }
  }
}
```

`hive-fast` is deliberately left out of this example; see "Known limitations."

**This full round trip is not yet confirmed working end to end.** With the
`/v1` correction above in place, `opencode run "..." -m hive/hive-default`
produced no output and no error at all within 45 seconds, at `--log-level
DEBUG`, and was killed rather than investigated further in this pass. The
URL-shape bug above is real and fixed in this doc, but treat the OpenCode
path as unverified until someone gets a real completion back through it.

## Known limitations (verified, not guessed)

- **`hive-fast` is currently broken on both `/v1/messages` and
  `/v1/chat/completions`, and its failure response leaks internal routing
  detail.** The alias's live route resolves to Groq's `llama-3.1-8b-instant`,
  which Groq now answers with `model_not_found` ("does not exist or you do
  not have access to it"), reproduced live 2026-08-18 on both surfaces. The
  gateway's own error message forwards that upstream text, plus LiteLLM's
  internal fallback-group bookkeeping (`Fallbacks=[{'hive-fast':
  ['hive-fast']}, ...]`, retry counts), verbatim into the customer-visible
  response. No literal provider name appears, so this survives the existing
  provider-name sanitizer, but the fallback-group internals are still
  implementation detail that should never reach a caller. Tracked as a
  separate defect from PR #954 (which this document otherwise verifies); use
  `hive-default` or `hive-auto` until it is fixed.
- **A console user who owns more than one workspace may have their default
  ("current") account be one with no billing mapping, and the console gives
  no warning about it.** A key minted from `/console/api-keys` while the
  session's default account has no `tenant_billing_accounts` row answers
  every `/v1/messages` request with `403 permission_error` /
  `account_not_provisioned` — the key looks completely normal at mint time.
  The console reads the target account from an `hive_account_id` cookie (sent
  as `X-Hive-Account-ID` to control-plane) if one is set, defaulting to the
  session's first membership otherwise; there is no UI affordance today that
  surfaces which of a multi-workspace user's accounts is "current" before a
  key is minted, or that warns a freshly minted key is unusable. Reproduced
  live 2026-08-18 (not part of PR #954's scope; filed separately).
- **Error envelope is Anthropic-shaped only for errors this surface itself
  raises or forwards from a resolved request** (bad model, bad request body,
  a resolved key's own refusal). A request that fails before it ever resolves
  a key at all (a session-only guard, a global rate limit) still returns the
  pre-existing generic envelope. The SDK still raises the correct exception
  **class** either way (`AuthenticationError`, `RateLimitError`, etc., driven
  by HTTP status), but `err.type` and `err.request_id` may be empty on that
  narrower set of errors.
- **Extended thinking, prompt caching, and server-side tools
  (`thinking`/`redacted_thinking` blocks, `cache_control`, `container`,
  `service_tier`, `inference_geo`) are not implemented.** Neither of Hive's two
  configured providers (OpenRouter, Groq) supports Anthropic's native versions
  of these, so this is a provider-capability gap, not an oversight. Sending
  these fields is silently ignored rather than erroring.
- **`top_k` is not passed through.** OpenAI-shaped chat completions (what
  every request is lowered to before dispatch) has no equivalent field.
- **`stop_reason` is never `"stop_sequence"` and `stop_sequence` is always
  null**, even when a custom stop sequence was actually what stopped
  generation. The underlying OpenAI-shaped `finish_reason` never distinguishes
  "stopped naturally" from "matched a stop sequence," so this cannot be
  recovered without a provider-side capability neither OpenRouter nor Groq
  exposes today.
- **A non-streaming request with a very large `max_tokens` may be rejected
  client-side by the SDK itself** before any request reaches Hive at all (the
  SDK estimates whether a non-streaming call could exceed ~10 minutes and
  raises `ValueError` rather than sending it). Use `stream=True` /
  `.messages.stream(...)` for long-running generations, which is standard
  Anthropic SDK guidance, not a Hive-specific requirement.

# Pointing a real Anthropic SDK at Hive

This is the exact, tested path for using an unmodified Anthropic SDK client
(or an Anthropic-Messages-compatible agent CLI) against Hive by changing only
the base URL and the API key. Every claim below was verified against the live
deployed gateway and the real, installed `anthropic` SDK (Python 0.122.0) on
2026-08-17, not assumed from memory. See
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

## Python

```python
from anthropic import Anthropic

client = Anthropic(
    base_url="https://api-hive.scubed.co",
    api_key="<API_KEY>",
)

message = client.messages.create(
    model="hive-fast",
    max_tokens=256,
    messages=[{"role": "user", "content": "Hello"}],
)
print(message.content[0].text)
```

Streaming:

```python
with client.messages.stream(
    model="hive-fast",
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
  model: "hive-fast",
  max_tokens: 256,
  messages: [{ role: "user", content: "Hello" }],
});
```

## Model aliases that actually work on /v1/messages

Live from `GET https://api-hive.scubed.co/catalog/models` (no auth required,
provider-blind by design — it never names the upstream provider):

| Alias | What it is |
| --- | --- |
| `hive-default` | Balanced default, stable |
| `hive-fast` | Low-latency, stable |
| `hive-auto` | Preview, widens internally when needed |

`hive-embedding-default`, `hive-stt`, `hive-tts` also appear in the catalog but
are not chat models and do not answer `/v1/messages`. An alias not in this
list, or a real upstream route id (e.g. anything starting `route-`), is
refused with a provider-blind 404 before any upstream dispatch happens — this
is enforced behavior, not a bug (see `TestMessages_RawLiteLLMRouteIDIsRefused
AndNeverDispatched`).

## Agent CLI config (OpenCode / Claude-Code-compatible clients)

Any CLI that lets you override an Anthropic-compatible provider's base URL and
key works the same way. For a config shaped like OpenCode's provider block:

```json
{
  "provider": {
    "hive": {
      "npm": "@ai-sdk/anthropic",
      "options": {
        "baseURL": "https://api-hive.scubed.co",
        "apiKey": "<API_KEY>"
      },
      "models": {
        "hive-fast": {},
        "hive-default": {},
        "hive-auto": {}
      }
    }
  }
}
```

## Known limitations (verified, not guessed)

- **`x-api-key` authentication requires this PR's fix to be deployed.**
  Before it, every `/v1/messages` request authenticated with the SDK's default
  `api_key=` construction 401s with `missing bearer`, regardless of key
  validity — reproduced live against the deployed box with the real SDK. If
  you hit that specific error, the fix has not deployed yet; using
  `auth_token=` instead of `api_key=` (forcing an `Authorization: Bearer`
  header) is the workaround, not a normal part of this integration.
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

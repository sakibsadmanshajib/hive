# Live proof: /v1/messages routed through SelectRoute and metered

Evidence for the fix that stops `POST /v1/messages` dispatching to LiteLLM on its
own. Captured 2026-07-28 against an isolated stack, not a shared one.

## Why this defect mattered

LiteLLM model names **are** route ids. `deploy/litellm/config.yaml` declares its
`model_list` entries as `route-openrouter-default`, `route-groq-fast` and
friends. The pre-fix handler took the caller's `model` field and POSTed it
straight at that proxy, so naming a route instead of a Hive alias skipped, in one
move:

* per-tenant model entitlement, enforced inside `routing.Service.SelectRoute`,
* the API-key alias allowlist, checked on the alias in `authz.CheckAccess`,
* prepaid credit reservation and settlement.

## Setup

One isolated stack, two edge-api binaries on it, everything else shared between
them: the same control-plane, the same LiteLLM (real, with the real provider
routes), the same Redis, the same database. The only variable is the edge-api
code.

| Target | Binary |
|---|---|
| `http://localhost:8098` | pre-fix, `origin/main` at 425ac853, plus only the support-matrix registration so the endpoint is reachable at all |
| `http://localhost:8099` | this branch |

Two throwaway principals were created for the capture and used by nothing else:

* a tenant with an OWNER member, for the session (JWT) principal, with
  `hive-default` hidden from it in `tenant_model_visibility` and `hive-fast`
  left entitled,
* a billing account with an `hk_` API key and a credit grant, for the API-key
  principal.

Every `user_id`, `tenant_id` and `account_id` printed in these captures has been
replaced with a synthetic placeholder. The same placeholder stands for the same
principal across every file, so the accounting rows in
[10](10-accounting-rows.md) still tie to the API-key probes in
[08](08-fixed-apikey-raw-route-id.md) and
[09](09-fixed-apikey-entitled-alias-metered.md).

The pre-fix binary had to be given the support-matrix registration too, because
without it `UnsupportedEndpointMiddleware` 404s `/v1/messages` before any handler
runs. That is a second defect this branch fixes: the Anthropic surface has been
unreachable since it shipped, which is the only reason the routing hole was
latent rather than live. Registering the endpoint without fixing the handler is
exactly what would have opened it.

## Results

| # | Binary | Principal | Request | Result |
|---|---|---|---|---|
| [01](01-prefix-session-raw-route-id.md) | pre-fix | session | `model=route-groq-fast` | **200, real completion** — the bypass, unrouted and unmetered |
| [02](02-fixed-session-raw-route-id.md) | fixed | session | `model=route-groq-fast` | 404 `model not found`, nothing dispatched |
| [03](03-fixed-session-raw-route-id-streaming.md) | fixed | session | same, `stream=true` | 404, and not delivered as a stream |
| [04](04-fixed-session-unentitled-alias.md) | fixed | session | `model=hive-default` (hidden from this tenant) | 403 `model not available for this workspace` |
| [05](05-fixed-session-entitled-alias.md) | fixed | session | `model=hive-fast` | 200 Anthropic message, `model` echoes the alias |
| [06](06-fixed-session-entitled-alias-streaming.md) | fixed | session | `model=hive-fast`, `stream=true` | 200 `text/event-stream`, full Anthropic event sequence |
| [07](07-prefix-apikey-rejected-outright.md) | pre-fix | API key | `model=hive-fast` | 401 — the pre-fix handler served no API-key principal at all |
| [08](08-fixed-apikey-raw-route-id.md) | fixed | API key | `model=route-groq-fast` | 404, and no reservation created |
| [09](09-fixed-apikey-entitled-alias-metered.md) | fixed | API key | `model=hive-fast` | 200, metered |
| [10](10-accounting-rows.md) | — | — | database read-back | one reservation, `finalized`, settled at the real token count |

## The refusals are provider-blind

Neither refusal names a provider, a route id, or any other tenant's models. 02
answers `model not found` and 04 answers `model not available for this
workspace`. The catalog's own route table never appears.

## Credits moved where they previously could not

From [10](10-accounting-rows.md): the proof account holds exactly one
`credit_reservations` row for the two API-key probes, and it belongs to the
alias, not the refused route id.

```text
status     | reserved_credits | consumed_credits | released_credits | terminal_usage_confirmed
finalized  | 10000            | 110              | 9890             | true
```

Credits were held before dispatch, then settled at 110, which is the upstream
token total (78 in + 32 out) and matches the `llm_traces` row for the same model.
`terminal_usage_confirmed = true` records that the settlement came from a real
upstream usage report rather than the flat pre-dispatch estimate, which is what
the `stream_options.include_usage` the handler now sets is for.

Before this change no such row could exist for this surface: it never called
accounting.

## One thing this capture found that is NOT fixed here

The first run of probe 09 was made from an account with no credits. The
control-plane correctly refused the reservation with `409 reservation exceeds
available credits`, and `apps/edge-api/internal/inference/orchestrator.go` logged
it as non-fatal and served the request anyway, because its error mapping matches
only on `429`, `budget` and `insufficient`. A 409 credit refusal therefore
results in free inference on every endpoint that shares this orchestrator, not
just this one. It is out of scope for this branch (it is a shared money-path
behaviour change affecting four endpoints, and deserves its own change and its
own proof), and it is reported separately.

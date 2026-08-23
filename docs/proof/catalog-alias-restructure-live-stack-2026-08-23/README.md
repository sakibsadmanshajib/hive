# Catalog alias restructure, live full-stack capture

PR #1007. Captured 2026-08-23 against a stack brought up for this capture.

## What kind of evidence this is

**A local full-stack capture, not a demo-box capture.** Every service in the
chain was built from this branch and run together on one host, and the
screenshots are of a real browser driving a real Open WebUI against them. It is
not a screenshot of the deployed demo box, and it should not be read as one.

It is the live counterpart to `docs/proof/catalog-alias-restructure-2026-08-22/`,
which is database-level only and says so. That transcript proves the migration
applies. This one proves a user sees and can use the result, which is the claim
a migration transcript cannot make.

## Why a local stack rather than the box

The migration is not on the demo box yet, and staging it there was refused
deliberately: that box is live and its data plane is not a scratch surface. The
hosted Supabase project the shared `.env` points at no longer resolves in DNS,
so there was no shared database to capture against either. A stack built from
the branch, on its own throwaway Postgres, is what remains, and it exercises the
same code paths.

## The chain that was actually exercised

1. A throwaway `pgvector/pgvector:pg17` Postgres, created empty, then
   `scripts/ci-throwaway-db.sh`: all 91 migrations executed, including
   `20260822_02_catalog_alias_restructure.sql`. Its own assertion confirms it,
   `throwaway database ready: 91 of 91 migrations executed`.
2. `scripts/ci-seed-api-key.sh` minted one tenant, account, API key, policy and
   credit grant on that database. Generated at run time, thrown away with the
   stack.
3. `control-plane` and `edge-api` images **rebuilt from this branch** and run
   against that database, with `litellm`, `redis`, `open-webui` and
   `caddy-owui`, on their own Compose project and their own ports.
4. `POST /internal/litellm/sync` regenerated LiteLLM's `model_list` from
   `public.provider_routes.provider_model` and restarted the gateway.
5. A browser signed in to Open WebUI, opened the model picker, selected an
   alias and sent a message.

## What each file shows

| File | Shows |
|---|---|
| `litellm-sync-result.txt` | The regenerated `model_list` and the model ids the running gateway serves. This is the load-bearing one: it is the config sync, not the checked-in seed. |
| `edge-api-v1-models.json.txt` | `GET /v1/models` end to end through edge-api, which is what the picker reads. |
| `catalog-and-metering.txt` | The catalog rows the stack served from, and the `usage_events` rows the chat turn wrote. |
| `browser-capture-log.txt` | The browser transcript: URLs visited, console output, and the model names the open picker rendered. |

The two screenshots are attached to the pull request as release assets rather
than committed here, per `.claude/skills/pr-visual-proof.md`: a
`raw.githubusercontent.com` URL pinned to this branch would 404 the moment the
branch is deleted on merge.

## What it establishes

* All six restructured aliases reach a user. The picker rendered
  `deepseek-v4-flash`, `deepseek-v4-pro`, `hive-auto`, `hive-default`,
  `hive-medium` and `hive-small`, plus the deprecated `hive-fast`, which is the
  back-compat requirement shown rather than asserted.
* The config sync regenerates from the database, so `provider_routes` owns the
  upstream model. Every route resolves to the model the migration set:
  `route-groq-small`, `route-groq-default` and `route-groq-fast` to
  `groq/openai/gpt-oss-20b`; `route-groq-medium` and `route-groq-auto` to
  `groq/openai/gpt-oss-120b`; the two DeepSeek routes to their OpenRouter slugs.
* The retired `route-openrouter-default` and `route-openrouter-auto` are absent
  from the regenerated config. They are `disabled` in `provider_routes` and the
  sync drops them, which is the behaviour the migration depends on.
* A route is not merely listed but live. `hive-small` was selected and answered,
  and `usage_events` recorded the turn against `hive-small` with real token
  counts and a credit delta. Routes on this project have been silently inert
  before; a served answer is what rules that out.

## Limits of this evidence

* It is not the demo box. Anything box-specific (its env, its volumes, its
  deployed image) is untested here. The deploy that carries this migration is
  still owed its own confirmation.
* The embedding, STT and TTS aliases are absent from the picker because Open
  WebUI lists chat models only. They were not exercised.
* Open WebUI ran with `ENABLE_LOGIN_FORM` and `ENABLE_SIGNUP` on, for a local
  throwaway account. The deployed configuration is OIDC-only and is unchanged
  by this; no repository file was edited to obtain the capture and no existing
  account was touched.
* It says nothing about whether the upstream models still exist at the provider
  beyond the one that answered. That is issue #965.

## Reproducing

Bring up a throwaway Postgres, run `scripts/ci-throwaway-db.sh` and
`scripts/ci-seed-api-key.sh` against it, then start `redis`, `litellm`,
`control-plane`, `edge-api`, `open-webui` and `caddy-owui` from
`deploy/docker/docker-compose.yml` with `SUPABASE_DB_URL` pointed at it and the
Supabase JWT variables left blank, and `POST /internal/litellm/sync`.

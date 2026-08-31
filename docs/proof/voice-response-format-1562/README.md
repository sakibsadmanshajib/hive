# Voice proof, issue #1562 and issue #1381

Captured in Chromium against a running Hive stack on 2026-08-30: throwaway
Postgres on the real migration chain, the real LiteLLM, real Groq, the forked
chat front end behind `caddy-owui`. The browser only ever talked to
`http://localhost:24503`, which is the Caddy front, and the capture asserts
that rather than assuming it.

Three captures, one flow, three builds.

| File | Build | What it shows |
| --- | --- | --- |
| `read-aloud-before-fix.png` | `origin/main` | Read Aloud fails, and the toast publishes `url='http://edge-api:8080/v1/audio/speech'` to the signed-in user |
| `read-aloud-error-redacted.png` | chat image patched, edge-api still `origin/main` | The same failure, now `url='[redacted]'`. The diagnostics survive, the internal address does not |
| `read-aloud-playing.png` | both fixes | Read Aloud succeeds: 200, 15385 bytes, and the browser's own `<audio>` element reports the clip decoded |

## What the first image settles

Issue #1562 reports that voice mode "calls edgeapi:8080/v1". It does not. The
chat front end is same-origin throughout, and the guard test
`apps/web-console/tests/unit/browser-origin-hosts.test.ts` now holds that.

What actually happened is in the first image: Open WebUI's audio router
stringified the aiohttp exception into the error it returned, aiohttp bakes the
request URL into that string, and the browser rendered it. The user was shown
the internal address, not sent to it. The 500 underneath is issue #1381,
`response_format`, and it is fixed in the same pull request.

## Reproducing

The captures are not wired into a workflow, because the stack they need does
not exist in one. `owui-nightly.yml`, the only lane that boots Open WebUI, is
blocked on the deleted Supabase Cloud project (its own comments say nothing
there has run since 2026-08-09), and `agent-visual-proof.yml` is scoped to
Cowork and needs an Apptainer runner. So this is a local bring-up, written down
here so the next person does not re-derive it.

The regression cover that DOES run in CI is `browser-origin-hosts.test.ts` in
the web-console unit job, `speech_response_format_test.go` in the edge-api Go
job, `test_owui_audio_error_leak.py` in `make test-scripts`, the drift `grep` in
`Dockerfile.open-webui`, and the SDK audio conformance suite in `ci.yml`'s
`live-integration` job, whose two expected-failure markers this change removes.

### Bring-up

Everything below runs from a worktree with Docker and nothing else installed.

1. **A throwaway database with the real schema.** The migration chain seeds the
   `hive-tts` and `hive-stt` routes, so a hand-built database would not do.

   ```
   docker run -d --name hive-voice-db -p 55432:5432 \
     -e POSTGRES_PASSWORD=postgres -e POSTGRES_DB=hive_ci pgvector/pgvector:pg17
   PGHOST=127.0.0.1 PGPORT=55432 PGUSER=postgres PGPASSWORD=postgres \
     PGDATABASE=hive_ci scripts/ci-throwaway-db.sh
   ```

   On a host with no `psql`, put a shim on `PATH` that runs the client in a
   container on the host network, mounting the worktree at its own absolute
   path so the script's absolute `-f` arguments resolve.

2. **An API key with credit**, which `OWUI_SHIM_KEY` needs:
   `scripts/ci-seed-api-key.sh` with the same libpq variables.

3. **An env file** shaped like `ci.yml`'s `live-integration` job: placeholder
   `SUPABASE_*` (nothing here calls the Supabase HTTP surface), the discard-port
   `S3_*` stubs, real `GROQ_API_KEY`, and `SUPABASE_DB_URL` pointing at the
   docker0 gateway. Use the `postgresql://` scheme, not `postgres://`:
   SQLAlchemy rejects the latter with `Can't load plugin:
   sqlalchemy.dialects:postgres` and Open WebUI will not boot.

4. **Bring up** `redis litellm control-plane edge-api open-webui caddy-owui`
   under a project name of your own and with `ports` overridden, because
   `litellm` has a fixed `container_name` and the published ports collide with
   any peer agent's stack on the same host. `ports: !override` in an overlay
   file does that without editing the shared compose file.

5. **Sync LiteLLM from the catalog**, the same call a deploy makes, or the
   `route-groq-tts` group will not exist:
   `POST /internal/litellm/sync` with `X-Internal-Token`.

6. **An account.** `Caddyfile.owui` 404s `/api/v1/auths/signup` deliberately, so
   create the user against the chat container directly and sign in through Caddy
   like a real user. The overlay needs `ENABLE_LOGIN_FORM`, `ENABLE_SIGNUP` and
   `DEFAULT_USER_ROLE=admin`, since there is no OIDC provider locally.

### The trap that will waste your afternoon

**Open WebUI caches synthesized speech.** `_tts_openai` writes a cache file keyed
on the request payload, so re-running the same prompt returns the cached clip and
reports `200` **even with the fix reverted**. It produced a false green twice
while these captures were being made. Every before-and-after pair here uses text
that had never been synthesized before. If you verify voice by repeating a prompt
you already ran, you are measuring the cache, not the gateway.

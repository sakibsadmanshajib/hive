# Visual proof: PR #1067, settings declutter P0

Captured against the branch's own built chat frontend image
(`hive-open-webui:v0.10.2-branded`, built from `fix/settings-declutter-p0`),
run in an isolated throwaway compose project (`pr1067proof`) with unique host
ports so it did not touch the shared checkout or any other agent's stack.

## Why the setup differs from a normal local run

The shared dev `.env`'s `SUPABASE_DB_URL`/`SUPABASE_URL` point at a Supabase
Cloud project that has since been deleted (self-hosted cutover, see
`.wolf/decisions.md`); the pooler answers "tenant/user ... not found" and
open-webui's pgvector RAG client crashes hard on that DSN at boot (unlike
edge-api/control-plane, which degrade gracefully with a WARNING and keep
serving). For this capture only, a throwaway local `pgvector/pgvector:pg16`
container replaced that DSN for every service, and Caddy's `/auths/signup`
block (D-014, intentional, defense in depth) was bypassed by hitting
open-webui's own container port directly, once, to bootstrap a single local
admin account (`ENABLE_SIGNUP`/`ENABLE_LOGIN_FORM` set to `true` only for this
throwaway, fresh-volume container; the real posture in `docker-compose.yml`
itself, SSO-only, `ENABLE_SIGNUP=false`, `ENABLE_LOGIN_FORM=false`, is
untouched). None of this touched any other agent's running containers or the
shared checkout's `.env`.

## Bootstrap

```
POST http://localhost:48082/api/v1/auths/signup
-> 200, role: admin
   token: REDACTED_BOOTSTRAP_TOKEN (local-only JWT, container torn down after capture)
```

## Captures

- `pr1067-01-chat-home.png`: authenticated chat home, confirms the session works.
- `pr1067-02-settings-general.png`: Settings modal rail = General, Interface,
  Audio, Data Controls, Account, About. No Connections tab.
- `pr1067-03-settings-account.png`: Account page shows only Name plus avatar.
  No password change, no API key section, no JWT copy, no bio/gender/DOB.
- `pr1067-04-search-connections.png`: typing "connections" into the settings
  search field returns "No results found" (confirms removal from the search
  index, not just the visible tab).
- `pr1067-05-advanced-params.png` / `pr1067-06-advanced-params-bottom.png`:
  full Advanced Parameters list, General tab, "Show" expanded. Full text dump
  below.

## Advanced Parameters, full rendered list (top to bottom)

```
Stream Chat Response, Stream Delta Chunk Size, Context Compaction Threshold,
Function Calling, Reasoning Tags, Seed, Stop Sequence, Temperature,
Reasoning Effort, logit_bias, max_tokens, top_k, top_p, frequency_penalty,
presence_penalty, repeat_last_n, think (Ollama), format (Ollama),
[Add Custom Parameter]
```

No `min_p`, `mirostat`, `mirostat_eta`, `mirostat_tau`, `tfs_z`,
`repeat_penalty`, `use_mmap`, `use_mlock`, `keep_alive`, `num_keep`,
`num_ctx`, `num_batch`, `num_thread`, or `num_gpu` anywhere in the rendered
list, matching the PR's claimed purge. The three deliberately-not-purged
keys (`repeat_last_n`, `think`, `format`) are present, matching the PR body.

## Settings rail, full text (from `document.body.innerText`)

```
Settings
Search
General
Interface
Audio
Data Controls
Account
About
Admin Settings
```

No `Connections` entry.

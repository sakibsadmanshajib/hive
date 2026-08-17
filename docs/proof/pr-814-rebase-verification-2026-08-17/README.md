# PR #814 rebase-verification proof, 2026-08-17

The demo box runs `main`, not this branch, and the catalog/routing code has
moved since this PR was opened on 2026-08-09 (Phase 20 provider-catalog
waves, pricing corrections). Rather than trust the existing 2026-08-09
`docs/proof/issue-792/*` captures against code that has since changed, this
directory is a fresh capture against the branch as rebased onto `main` at
`4e39de40`.

## Method

Same posture as the PR #909 demo-surface proof (`docs/proof/pr-909-demo-surface-2026-08-16/`):
two `deploy/docker/Dockerfile.open-webui` images, built from the same pinned
upstream digest, differing only in whether the `owui-patches/` tree carries
this PR's fix. Both run standalone (`WEBUI_AUTH=false`, no Postgres, no
Supabase) against a stub gateway on an isolated Docker network, answering
`GET /v1/models` with the real six-alias Hive catalog shape
(`hive-auto`, `hive-default`, `hive-fast`, `hive-embedding-default`,
`hive-stt`, `hive-tts`). `BYPASS_MODEL_ACCESS_CONTROL=true` matches the
deployed compose value.

- `picker-main.png` / `picker-main.log` -- image built from `origin/main`
  (no picker patch). Model selector dropdown, opened and screenshotted.
- `picker-branch.png` / `picker-branch.log` -- image built from this branch
  (`HIVE_PICKER_HIDDEN_MODEL_IDS=hive-embedding-default,hive-stt,hive-tts`).

## Result

| | dropdown contents |
| --- | --- |
| `main` (unpatched) | `hive-auto`, `hive-default`, `hive-fast`, `hive-embedding-default`, `hive-stt`, `hive-tts` |
| this branch (patched) | `hive-auto`, `hive-default`, `hive-fast` |

The `.log` files are the DOM read the capture script made (which ids from
the known six actually render in the opened dropdown), not just the
screenshot, same double-check pattern PR #909 used for its user-menu proof.

## What this does not re-verify

This is the picker only. `docs/proof/issue-792/probe-*.json` (unchanged by
this rebase) is still the evidence that RAG ingest/query, chat, TTS and STT
are unaffected by the fix and that the gateway's own `/v1/models`-shaped
list keeps serving all six aliases; nothing in this rebase touched that
code path.

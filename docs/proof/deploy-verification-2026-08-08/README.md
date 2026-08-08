# Demo box deploy verification, 2026-08-08

Live verification of the seven PRs merged to `main` on 2026-08-08 (751, 753, 774,
776, 777, 779, 786), captured against the demo box after deploy-demo-box run
[31268325631](https://github.com/sakibsadmanshajib/hive/actions/runs/31268325631)
(`workflow_dispatch` on `main` at `c9166429923dced43354b9267f006e7640595f2b`,
conclusion **success**).

## Why a dispatch was needed at all

PR #786 changes only `apps/web-console/**`, which is not in
`.github/workflows/deploy-demo-box.yml`'s `paths:` filter. Merging it therefore
triggered no deploy, and its change sat on `main` undeployed. This is the same
blind spot the `supabase/migrations/**` entry was added for. The manual dispatch
is what put it on the box, confirmed by the build log: the `web-console-prod`
image ran `COPY apps/web-console/ ./` and `RUN npm run build` as cache misses
(`#81 DONE 0.2s`, `#82 DONE 47.4s`), then `Container hive-web-console-prod-1
Recreated`.

## How the running image was identified

Not from the build log, which only proves what the builder intended. Every entry
in `deploy/docker/owui-patches/hive_ui_surfaces.py` rewrites a verbatim substring
of the compiled Svelte bundle at image build time, so the bundle Open WebUI
actually serves is a fingerprint of the image. `bundle-verdict.json` records a
crawl of all 788 immutable JS assets on `chat-hive.scubed.co`:

- 13 of 13 rewrites present in patched form, 0 in pre-patch form
- 3 of 3 guard strings (surfaces Hive keeps) intact

The two forms are mutually exclusive at each site, and the pre-patch text is what
the upstream image ships, so this check cannot pass against the previous image.

## How the Caddy config was identified

The deploy's own `/v1/featuregate` assertion passes, but it would also have
passed before this change, so it proves nothing new on its own. `routes.json`
records the check that does: every path first blocked by #774, #777 and #779
answers with Caddy's zero-length, no-content-type 404, across case, trailing
slash, duplicate slash, percent-encoded and sub-path variants. Under the previous
config those paths reached Open WebUI and answered `401 {"detail":"Not
authenticated"}` or served the SPA document.

Positive controls ran on the same client so a Cloudflare bot block could not be
mistaken for a pass: `/api/v1/configs/banners` returns app JSON, `/api/v1/auths/
signin` returns FastAPI's `422 Field required`, `/api/config` returns 200 JSON,
and the SPA root returns HTML. Cloudflare interference is real on this origin and
was observed during this run: a default `python-urllib` User-Agent gets
`403 error code: 1010`, which reads like an auth failure and proves nothing. All
probes therefore send a browser User-Agent.

## Results

| Claim | Verdict | Artifact |
| --- | --- | --- |
| `/api/v1/configs/export` and `/openai/config` 404 on the chat origin | PASS | `routes.json` |
| Terminal proxy and tool server registration blocked, all path variants | PASS | `routes.json` |
| Chat login page presents only Continue with Hive | PASS | `01-chat-login-page.png` |
| Model picker hides embedding / STT / TTS aliases | **FAIL** | `03-model-picker-open.png` |
| `/v1/models` still returns all six aliases | PASS | `api-v1-models.json` |
| Workspace shows only Hive surfaces, Skills tab gone | PASS | `04-workspace-tabs.png` |
| Settings has no Integrations tab | PASS | `05-settings-tabs.png` |
| About tab Hive-branded, no upstream credit or social row | PASS | `06-settings-about.png` |
| No changelog dialog on first load | PASS | `02-first-load-no-changelog.png` |
| Console workspace switcher opens and lists workspaces | PASS | `09-console-switcher-multi-open.png` |
| Switcher non-interactive for a single membership | PASS | `10-console-switcher-single.png` |
| Chat completes a message end to end | PASS | `07-chat-end-to-end.png` |

## Credential handling

No password was used: the stored demo password no longer authenticates and was
deliberately not rotated, since other agents hold it. Sessions were established
by minting a one-shot OTP through the Supabase admin API and exchanging it on the
ordinary `/auth/v1/verify` path, which changes no account credential.

Captures are headless, so no screenshot contains a browser chrome URL bar. Every
URL written to a log passes through a redactor covering query strings, the
implicit-grant `#access_token=` fragment, and percent-encoded copies carried
inside a `next=` parameter. The fragment case was added after it was observed
live: a query-only redactor printed a real session token.

Two API keys minted for the `/v1/models` check were revoked in the same run.

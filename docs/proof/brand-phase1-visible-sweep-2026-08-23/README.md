# Phase 1 brand sweep visual proof (PR #1071)

Captured 2026-08-23 against a local stack built from `fix/brand-phase1-visible-sweep`
(HEAD `0a5b2b4c533c5fdd5111e95a38fcdf1ab62d82c8`), not against the demo box.
Nothing was deployed and no running service was touched. Setup summary, curl
output, DOM text checks, and the completeness/reachability audit are in
`capture-log.txt`. The setup section is a prose summary of the stack and
overrides, not a literal command transcript: the exact `docker compose`
invocations carried a per-session private image tag and locally generated
throwaway secrets, neither of which are meaningful to commit verbatim.

## Stack under test

| Component | What it was |
| --- | --- |
| Open WebUI | `Dockerfile.open-webui` built from this branch's `vendor/open-webui`, tagged privately (`hive-open-webui:brand1071-verify`) to avoid the repo's shared `hive-open-webui:v0.10.2-branded` tag, which a concurrent agent's build overwrote mid-session (see the buglog entry in the PR body). |
| edge-api / control-plane | Running DB-degraded: the repo's shared cloud Supabase project is decommissioned (self-hosted migration, `.wolf/decisions.md`), and neither service is required for the frontend surfaces this PR touches. |
| Auth | Local admin signup (`ENABLE_SIGNUP` flipped) reached through open-webui's backend port directly, since `Caddyfile.owui` blocks `/api/v1/auths/signup` unconditionally at the real ingress by design (SSO-only in production). No credential in any captured URL. |
| Vector store | Overridden to the bundled Chroma store so the container boots without the decommissioned Postgres; RAG is not exercised. |

## Files

| File | What it shows |
| --- | --- |
| `capture-log.txt` | Setup summary, curl output for `<title>` and `opensearch.xml`, DOM text checks, and the completeness/reachability audit beyond the diff. |
| `01-onboarding-get-started-with-hive.png` | Pre-auth onboarding screen: "Get started with Hive" (was "Get started with Open WebUI"). |
| `02-logged-in-home-hive-sidebar.png` | Logged-in chat home; sidebar reads "Hive". |
| `03-admin-settings-general-help-text.png` | Admin Settings > General: "Discover how to use Hive and seek support from the community." (was "...use Open WebUI..."). |
| `04-add-tool-server-mcp-warning.png` | Admin Settings > Integrations > External Tool Servers > Add Connection, Type=MCP: "...directly maintained by the Hive team..." (was "...the Open WebUI team..."). |
| `05-external-knowledge-connection-modal.png` | Admin Settings > Integrations > External Knowledge Sources > Add Knowledge Connection: "...configured in Hive." (was "...configured in Open WebUI."). |

## Scope notes surfaced by this pass

- Non-English locale files (~60) still carry the same 17 strings untranslated from upstream. This PR's stated scope is en-US only; deferring the rest is a deliberate, separate phase (see PR body).
- `workspace/Models.svelte`, `workspace/Prompts.svelte`, and `workspace/Tools.svelte` are edited by this PR but have zero importers anywhere in the current tree (a prior merged change already redirects `/workspace` straight to `/workspace/knowledge` and removed their nav). The fix is correct but not independently demonstrable live; see PR body.
- `workspace/common/ManifestModal.svelte` is also edited and is reachable via `admin/Functions.svelte` (a live route); not screenshotted here since exercising it needs an installed function with a `funding_url` manifest field, and the string change is already covered by the i18n lockstep check in `capture-log.txt`.

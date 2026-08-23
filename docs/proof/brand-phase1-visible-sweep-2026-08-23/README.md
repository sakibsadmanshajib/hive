# Phase 1 brand sweep visual proof (PR #1071)

Captured 2026-08-23 against a local stack built from `fix/brand-phase1-visible-sweep`,
rebased twice onto `main` after this proof was first captured (see "Rebase
history" below), not against the demo box. Nothing was deployed and no
running service was touched. Setup summary, curl output, DOM text checks,
and the completeness/reachability audit are in `capture-log.txt`. The setup
section is a prose summary of the stack and overrides, not a literal command
transcript: the exact `docker compose` invocations carried a per-session
private image tag and locally generated throwaway secrets, neither of which
are meaningful to commit verbatim.

## Stack under test

| Component | What it was |
| --- | --- |
| Open WebUI | `Dockerfile.open-webui` built from this branch's `vendor/open-webui`, tagged privately (`hive-open-webui:brand1071-verify`) to avoid the repo's shared `hive-open-webui:v0.10.2-branded` tag, which a concurrent agent's build overwrote mid-session (see the buglog entry in the PR body). |
| edge-api / control-plane | Running DB-degraded: the repo's shared cloud Supabase project is decommissioned (self-hosted migration, `.wolf/decisions.md`), and neither service is required for the frontend surfaces this PR touches. |
| Auth | Local admin signup (`ENABLE_SIGNUP` flipped) reached through open-webui's backend port directly, since `Caddyfile.owui` blocks `/api/v1/auths/signup` unconditionally at the real ingress by design (SSO-only in production). No credential in any captured URL. |
| Vector store | Overridden to the bundled Chroma store so the container boots without the decommissioned Postgres; RAG is not exercised. |

## Rebase history and what it changed about this proof

This branch merged `main` twice after the original capture: once for #1067
(deleted `chat/Settings/Connections.svelte`, one line of this PR's own diff)
and #1085 (unrelated frontend build fix), and again for #1091 ("close the
public chat origin's admin surface"), which deletes the entire
`routes/(app)/admin/` route tree and the `SettingsModal` "Admin Settings"
link. That second merge made six `lib/components/admin/**` edits and the
`workspace/Models.svelte` / `Prompts.svelte` / `Tools.svelte` /
`common/ManifestModal.svelte` edits from this PR unreachable via any route
(reachability re-audited with a real import-path grep, not a basename
match, which had produced false positives earlier in this same pass). Those
edits were reverted to `main`'s content rather than kept as unreachable
"fixes," and `translation.json` was trimmed to the 8 en-US keys a live
surface still references. Three screenshots from the original capture
(admin settings general, the MCP warning reached through Admin Settings,
and the external-knowledge modal reached through Admin Settings) proved
surfaces that no longer exist and were removed; the AddToolServerModal MCP
warning is still confirmed live below through a different, non-admin path,
and the corresponding screenshot is not re-captured here for time, per the
completeness log.

## Files

| File | What it shows |
| --- | --- |
| `capture-log.txt` | Setup summary, curl output for `<title>` and `opensearch.xml`, DOM text checks, the completeness/reachability audit beyond the diff, and the final reachability correction after the admin-surface lockdown. |
| `01-onboarding-get-started-with-hive.png` | Pre-auth onboarding screen: "Get started with Hive" (was "Get started with Open WebUI"). Unaffected by the admin lockdown. |
| `02-logged-in-home-hive-sidebar.png` | Logged-in chat home; sidebar reads "Hive". Unaffected by the admin lockdown. |

## Scope notes surfaced by this pass

- Non-English locale files (~60) still carry the same 8 remaining strings untranslated from upstream. This PR's stated scope is en-US only; deferring the rest is a deliberate, separate phase (see PR body).
- `workspace/Models.svelte`, `workspace/Prompts.svelte`, `workspace/Tools.svelte`, `workspace/common/ManifestModal.svelte`, and six `lib/components/admin/**` files (`admin/Evaluations/Feedbacks.svelte`, `admin/Functions.svelte`, `admin/Settings/{Audio,Events,ExternalKnowledge,General}.svelte`) were edited earlier in this PR's history but are reverted as of the final commit: none has a live importer after #1091 deleted the admin route tree (`admin/Functions.svelte` was `ManifestModal.svelte`'s last remaining live path). See PR body for the full accounting.

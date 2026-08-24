# Console provider controls, visual proof

Branch feat/console-provider-controls. Captured 2026-08-24 against a local
full stack built from this branch (throwaway Postgres + GoTrue/PostgREST
gateway + branch control-plane image + branch web-console under next dev).
Setup summary and the browser transcript are in `capture-log.txt`. The
screenshots are also attached to the pull request as release assets per
`.claude/skills/pr-visual-proof.md`.

| File | Shows |
|---|---|
| `01-providers-list.png` | New Admin > Providers page: register form (slug, display name, base URL, API key env var NAME, optional LiteLLM prefix) plus the registry list rendering exactly the fields the backend stores, including a seeded disabled row. |
| `02-providers-edit-open.png` | Inline edit form open on a row, prefilled from the record. |
| `03-providers-edited.png` | List after a saved edit (display name changed through the full-replace PUT). |
| `04-providers-disabled.png` | Enable toggle off: Disabled badge, switch state, and the row re-enabled again in the transcript. |
| `05-analytics-observability.png` | Analytics page with the new Observability tile row: request-logs link plus the two Grafana tiles in their explicit not-configured state (GRAFANA_BASE_URL unset on this stack; no fake URL photographed). |
| `06-providers-nonadmin.png` | The same providers page as a signed-in, verified, non-platform-admin: "Managed by your administrator" access state instead of the manager. |
| `07-logs-browser.png` | The existing request log browser the Request logs tile links to. |

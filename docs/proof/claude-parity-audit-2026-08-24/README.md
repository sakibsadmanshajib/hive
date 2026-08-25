# Live Hive captures, 2026-08-23: chat and console

Thirty-four captures of the running product, taken 2026-08-23 between 20:25 and 20:48 against
`https://chat-hive.scubed.co` and `https://console-hive.scubed.co`, signed in as the shared
E2E fixture account through the admin one-time-token flow (`docs/live-test-auth.md`). No
password was set, reset or rotated to obtain the session.

These are preserved here because they are the last verified visual record of the live chat
shell. They were produced inside an unmerged agent worktree, which a prune would have
destroyed, and two later documents depend on them as evidence: the functional audit
`session-2026-08-24-claude-parity-audit` and the design scorecard
`session-2026-08-25-claude-similarity-scorecard`, both in the project vault. The vault, not
this directory, is authoritative on what the captures mean.

## What each image is

| File | Surface |
|---|---|
| `smoke-chat-home.png` | Chat sign-in consent screen, control-plane OAuth |
| `smoke-console-home.png` | Console Overview |
| `01-home-chat-mode.png` | Chat home, empty state: sidebar, headline, composer |
| `02-model-menu.png` | Model picker open, All and External tabs |
| `03-chat-stream-reply.png` | Chat reply rendered, reasoning row and message toolbar |
| `04-agents-surface.png` | `/agents` destination, service disabled for the tenant |
| `04b-agent-workspace.png` | Agent workspace, its own separate sign-in form |
| `05-upload-corrupt-docx.png` | Corrupt docx attached, inline error toast |
| `06-upload-empty-csv.png` | Chat home after an empty CSV attach attempt |
| `06b-empty-csv-sent.png` | Empty CSV sent, reply pending |
| `07-upload-50mb-sent.png` | 50 MB PDF attached, send pressed |
| `08-upload-after-reload.png` | Attachment chip after a reload |
| `08-upload-midupload-reload.png` | Attachment chip surviving a mid-upload reload |
| `09-knowledge.png` | Result of clicking the Knowledge nav row |
| `10-websearch-more-menu.png` | Composer integrations menu, one entry |
| `11-settings-modal.png` | Chat settings modal, General pane |
| `13-code-interpreter-run.png` | Code interpreter run, inline tool line and result |
| `14-console-catalog.png` | Console model catalog |
| `15-console-logs.png` | Console request logs, empty |
| `16-console-analytics.png` | Console usage and analytics |
| `17-console-billing.png` | Console billing |
| `18-console-members.png` | Console members |
| `19-console-settings.png` | Console profile settings |
| `20-console-feature-gates.png` | Console feature gates |
| `21-console-marketplace.png` | Console MCP and skills marketplace |
| `22-chat-artifacts-route.png` | `/artifacts` route, indefinite loading spinner |
| `23-chat-scheduled-route.png` | `/scheduled` route, unstyled 404 |
| `24-chat-projects-route.png` | `/projects` route, unstyled 404 |
| `25-chat-workspace-knowledge.png` | `/workspace/knowledge` route, bounced to chat home |
| `26-chat-tools.png` | `/tools` route, unstyled 404 |
| `27-console-api-keys.png` | Console API keys, key column redacted (see below) |
| `28-paste-100kb.png` | 100 KB paste accepted into the composer |
| `29-back-midstream.png` | Conversation after a back-button press mid-stream |
| `30-rapid-messages.png` | Sidebar after fourteen rapid messages |

## Redaction

Every image was reviewed before being committed. One carried credential-shaped content:
`27-console-api-keys.png` showed the console's own masked key strings, which print a fixed
prefix, an elision, and a short suffix. Six of the seven rows are revoked keys and one is
active. The suffix is not enough to reconstruct a key, but a fragment of a live credential
does not belong in a committed capture, so the entire KEY column was painted over before
the file was added; the column header and every other column are untouched. No other image
contains a token, an authorization code, a session value, or a URL with a credential in its
query string, because these captures are viewport-only and carry no address bar.

The account address visible in the chat sidebar and the console account row is the shared
E2E fixture, which is already documented in `docs/live-test-auth.md` and in earlier proof
directories in this repository. It is not customer data and is not treated as a secret here.
The workspace, owner name and balances shown throughout the console captures are E2E fixture
values, not a real tenant.

## Staleness

These captures are the live state of 2026-08-23. Work has merged since that no screenshot in
this directory reflects, including the Projects destination (#1117), the Scheduled sidebar
row (#1118), the composer credits banner (#1119), the composer size-failure fix (#1112) and
the `/artifacts` index (#1141). Anything scored against this directory is scored against the
2026-08-23 state, and a fresh capture is required before claiming any of those five landed
changes looks the way it is supposed to.

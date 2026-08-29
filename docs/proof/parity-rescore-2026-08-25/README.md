# Parity re-score captures, 2026-08-25

Thirty-one screenshots and three capture logs taken against the deployed demo box on
the evening of 2026-08-25 (22:10 to 22:20 UTC), during the live re-score of the Claude
Desktop and OpenRouter console similarity scorecards. They are committed here because
they previously existed only in a scratch directory and would have been lost to a prune.

## Staleness caveat, read this first

**These captures do not show the product as it is today.** They were taken against the
box as it stood on 2026-08-25. A QA and parity pass on 2026-08-29, against commit
`5e08d641`, found that several of the surfaces below have since changed materially:

- The chat home in `k01-home.png` and `k12-account-menu.png` shows a model-slug headline
  (`Hive Auto`) with the composer pinned to the bottom of the window. The deployed home
  now renders a personalised greeting and four quick-start chips, optically centred.
- The sidebar in every `k*` capture carries an `Agents` nav row. That row has since been
  removed, and `Artifacts` has been added in its place.
- The composer in every `k*` capture has no `Chat | Cowork` segmented control. The
  deployed composer now has one, and it switches the composer into a sandbox-run mode.
- `c05-catalog.png` shows a catalog with no search, no capability filter and no sort
  control. All three now exist, alongside cache-read and cache-write pricing columns.
- `c04-api-keys.png` shows a key-creation form with a nickname and an expiry only. The
  form now also carries a per-key credit limit and a limit reset cadence.
- `c11-providers.png` shows `/console/providers` rendering a `Provider endpoints` page.
  That route now deliberately returns 404 as the access-control fix for issues #947,
  #948 and #949.

Treat these images as the "before" half of a before-and-after pair, not as a record of
current behaviour. The 2026-08-29 measurement is the current record.

## Redaction

Every frame was reviewed before commit. The account email is painted out in each frame
that carried it, and the composer credits banner, which carries a live balance, is
painted out in each chat frame. API key values were already masked by the product
itself (`hk_xxxx•••`) and no frame carries a full secret. No URL in any capture or log
carries a credential in its query string.

## Chat captures (chat-hive.scubed.co)

| File | What it shows |
| --- | --- |
| `k01-home.png` | Chat home, light, model-slug headline, composer pinned bottom |
| `k02-model-menu.png` | Model menu opened from the header |
| `k04-streaming-midflight.png` | A reply mid-generation |
| `k05-reply-rendered.png` | A completed reply exercising every markdown primitive |
| `k06-reply-fullpage.png` | The same reply, full-page capture |
| `k07-projects.png` | `/projects` index with its card grid |
| `k08-artifacts.png` | `/artifacts` empty state, showing the top-edge placement defect |
| `k09-knowledge.png` | `/knowledge` destination |
| `k10-scheduled.png` | `/scheduled` route |
| `k11-bogus-route.png` | In-shell 404 card for an unknown route |
| `k12-account-menu.png` | Account row menu, open |
| `k13-settings-modal.png` | Settings modal, two-pane |
| `k20-model-menu-composer.png` | Model menu re-anchored to the composer chip |
| `k21-agents.png` | The Agents destination, since removed from the nav |
| `k22-scheduled.png` | Scheduled empty state with its line-art clock |
| `k23-knowledge-nav.png` | Knowledge reached from the nav row |
| `k24-conversation-resting.png` | A conversation at rest |
| `k25-conversation-hover-toolbar.png` | The same conversation with the pointer over an assistant turn |
| `k26-dark-home.png` | Chat home in the dark register |
| `probe-chat.png` | Session probe frame |

## Console captures (console-hive.scubed.co)

| File | What it shows |
| --- | --- |
| `c01-overview.png` | `/console` overview |
| `c02-logs.png` | `/console/logs` request log with filters and CSV export |
| `c03-analytics.png` | `/console/analytics` overview tab |
| `c04-api-keys.png` | `/console/api-keys` list and creation form |
| `c05-catalog.png` | `/console/catalog` model list |
| `c06-model-detail.png` | `/console/catalog/deepseek-v4-flash` detail page |
| `c07-billing.png` | `/console/billing` balance and recent transactions |
| `c08-docs.png` | `/console/docs` API reference |
| `c09-billing-alerts.png` | `/console/billing/alerts` spend alerts |
| `c10-settings-profile.png` | `/console/settings/profile` |
| `c11-providers.png` | `/console/providers`, before it was made to return 404 |

## Capture logs

`chat-capture.log`, `chat-capture-2.log` and `console-capture.log` carry the URL, HTTP
status and first heading or body excerpt for each frame. They are committed here rather
than left beside the images in a scratch directory because `npm run lint:proof-tokens`
scans `docs/proof/` and nothing else; a log kept anywhere else is unscanned.

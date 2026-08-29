# QA matrix and parity captures, 2026-08-29

Thirty-five screenshots and one capture log taken against the deployed demo box on
2026-08-29 between 08:15 and 09:20 UTC, during a full QA pass over the console and chat
surfaces and a re-score of the two similarity scorecards.

## What was measured, and against which build

Every frame was taken against `https://console-hive.scubed.co` and
`https://chat-hive.scubed.co`, signed in as the demo account through the admin
one-time-token flow. No password was set, reset or rotated.

The box moved during the pass. It served commit `5e08d641` when the walk started, and a
deploy carrying `f63ee8e3` and `c9e1419b` (pull request #1375, a console table, chart and
chat front end defect batch) landed at roughly 08:47 UTC. Every finding that this
directory illustrates was re-verified against `c9e1419b` after that deploy, and the
frames here are the post-deploy captures except where the filename says otherwise.

## Defect evidence

| File | What it shows |
| --- | --- |
| `01-chat-composer-smart-typography.png` | The composer after typing `const s = "hi"; // a--b 'x' ... "q"`. Straight quotes have become curly, the double hyphen an em dash and the ellipsis a single glyph |
| `02-console-api-keys-table-broken-by-long-nickname.png` | The API keys table after one key was created with a 5000-character nickname. Every column after NAME is pushed out of reach |
| `03-console-feature-gates-stuck-saving.png` | All twenty-five feature-gate toggles reading `Saving…`, twenty seconds after load and never resolving |
| `04-console-analytics-spend-by-key-raw-uuids.png` | The Spend tab grouped by API key, listing raw key identifiers where the Overview tile lists names |
| `05-console-analytics-375-overflow.png` | Analytics at 375 wide; the cards render 411 pixels wide and the main region scrolls sideways |
| `06-chat-upload-30mb-stalled.png` | A 28.6 MB attachment chip after 105 seconds. The upload request never returned and nothing on screen says so |
| `07-console-buy-credits-inert.png` | The billing page after clicking Buy credits. The URL gained `?action=buy`, nothing else changed |
| `08-console-billing-checkout-404.png` | `/console/billing/checkout` returning the 404 page |
| `09-console-404-outside-shell.png` | An unknown catalog route rendering a bare 404 with no console navigation |
| `10-console-analytics-overview-blended-price.png` | The blended price tile, whose subtitle prints a bare credits figure that reads as an account balance |
| `35-console-corrupt-session-redirects-to-sign-in.png` | A deliberately corrupted session cookie producing a clean redirect to sign-in rather than an error |

## Parity and capability evidence

| File | What it shows |
| --- | --- |
| `11-chat-home-1440-greeting-and-chips.png` | Chat home with a personalised greeting, four quick-start chips and the composer at optical centre |
| `12-chat-home-375.png`, `13-chat-drawer-375.png` | The same home at 375 wide, and the navigation drawer opened |
| `14-chat-model-picker.png` | The model menu anchored to the composer chip, with display names and purpose subtitles, no longer clipped at the window edge |
| `15-chat-cowork-toggle.png`, `16-chat-cowork-run-in-conversation.png` | The `Chat | Cowork` segmented control, and a submitted run rendering inside the conversation |
| `17-chat-projects.png`, `18-chat-artifacts.png`, `19-chat-schedules.png` | The three nav destinations |
| `20-chat-404-in-shell.png` | An unknown chat route rendering a 404 card inside the shell |
| `21-chat-settings-general.png` to `26-chat-settings-interface.png` | Every settings pane, including the system prompt field, the full advanced-parameter list, the usage pane and the data controls pane |
| `27-chat-system-prompt-reaches-model.png` | A reply of `BANANA` to `What is 2+2?`, proving the settings system prompt reaches the model rather than only the request |
| `28-chat-attachment-reaches-model.png` | The model quoting a code from an attached file, with a source citation |
| `29-console-overview.png` to `34-console-logs-1024.png` | Console surfaces at 1440, 1024 and 375 |

## Redaction

Every frame was reviewed before commit. No frame carries an API key secret: the product
masks stored keys as `hk_xxxx•••` and the three frames that showed a freshly created key
in full were deleted rather than committed. No URL in any frame or in
`walk-capture.log` carries a credential in its query string. The account address
`demo@hive-demo.invalid` appears in some frames; it is a reserved non-routable address
already documented in this repository and it is not a credential. The demo account's
credit balance appears in several frames and is likewise not a credential.

## Cleanup performed

The pass created API keys in order to exercise the creation form. All of them were
revoked, and the four whose nicknames would have degraded the API keys table were
shortened afterwards so the demo surface was left as it was found. One key belonging to
a different account still carries a 5000-character nickname from an earlier probe; it
was left alone because it is not this pass's state to change.

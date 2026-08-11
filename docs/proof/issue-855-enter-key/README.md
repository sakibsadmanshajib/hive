# Issue #855: Enter did not send in the chat composer

Captured live against `https://chat-hive.scubed.co` on 2026-08-11 with
`capture.mjs` in this directory, signed in as the shared demo account through
`apps/web-console/tests/e2e/support/live-auth.mjs`. No URL in this journey
carries a credential, so nothing in these images or logs needed redacting.

## Root cause

Not upstream Open WebUI, and not the Hive patch layer. The account had
`settings.ui.ctrlEnterToSend` set to `true`, which is the per-user preference
behind Settings > Interface > Input > "Enter Key Behavior". With it on, Enter
inserts a newline and only Ctrl+Enter sends, while the send arrow keeps working
either way. That is exactly the reported asymmetry.

Both the stock `ghcr.io/open-webui/open-webui:v0.10.2` image and the Hive
patched image were run locally first and sent on Enter in both cases, which
ruled out the image before anything was changed.

Nothing in Hive writes that key. An automated sweep over the settings surface
of the shared demo account left it set, and it stayed set for days because
nothing checks it. `apps/web-console/scripts/demo-chat-settings.mjs` is that
check now.

## Before the repair

`before.log`, with `settings.ui.ctrlEnterToSend: true`:

- `before-1-typed.png` — prompt sitting in the composer.
- `before-2-after-enter.png` — after Enter: still the landing screen, prompt
  still in the composer, `POST /api/chat/completions` count 0.
- `before-3-shift-enter.png` — Shift+Enter added lines without sending, which
  it must keep doing after the fix.

## After the repair

`after.log`, with `settings.ui.ctrlEnterToSend: false`:

- `after-1-typed.png` — same prompt in the composer.
- `after-2-after-enter.png` — after Enter: one `POST /api/chat/completions`,
  and the assistant turn reads `HIVE ENTER PROBE`, the exact string the prompt
  asked for. The assertion is on that answer text, not on a Copy button: an
  error bubble renders a Copy button too.
- `after-3-shift-enter.png` — Shift+Enter still inserts a newline and still
  does not send, with a `POST` count of 0 for that step.

Both runs delete the chats they created before exiting, so this leaves no new
litter on the account (issue #848).

## Re-checking it

```bash
cd apps/web-console
node scripts/demo-chat-settings.mjs <demo account email>            # check, exit 1 on drift
node scripts/demo-chat-settings.mjs <demo account email> --repair   # correct it
```

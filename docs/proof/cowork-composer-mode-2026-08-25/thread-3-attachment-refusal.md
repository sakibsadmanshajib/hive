---
type: proof
date: 2026-08-25
source: PR #1193, CodeRabbit thread 3 fix
---

# Thread 3 fix: Cowork mode refuses attachments and blank instructions

CodeRabbit thread 3 on PR #1193 (`vendor/open-webui/src/lib/components/chat/Chat.svelte`,
line 2286) flagged two related defects: an attached file was silently dropped
in Cowork mode (the run backend accepts only `pack` and `instructions`, with
nowhere for a file to go), and a submission with an empty prompt plus a
non-empty file list created a task with blank instructions. The fix blocks
both cases in `submitHandler` before the composer clears its input, so
nothing is lost: the file and the typed text both stay in the composer.

## Substrate

Standalone Open WebUI container, not the live demo box. Built from this
branch with `docker build -f deploy/docker/Dockerfile.open-webui`, run on a
copy of an already-signed-in local data volume (`composerfix-owui-data`),
against a stub LLM backend on the same docker network. Signed in with a
short-lived admin token minted by `scripts/owui-mint-admin-token.py`
(read-only against the container's own sqlite DB, no password touched). This
guard fires client side, before any network call, so no live agent-task
backend was needed to exercise it.

## Capture

* `t1193-01-signed-in-home.png` - signed in as the seeded admin account, chat
  home with the `Chat | Cowork` composer control.
* `t1193-02-cowork-mode-selected.png` - Cowork selected, second row present.
* `t1193-03-file-attached.png` - a `.txt` file attached in Cowork mode,
  uploading.
* `t1193-04-attachment-refused-toast.png` - typed "Please read this file" and
  clicked send with the file still attached. Toast: "Attachments are not
  supported in Cowork mode yet. Remove the file, or switch to Chat mode to
  send it." Both the file chip and the typed text remain in the composer;
  nothing was sent and nothing was cleared.

No credential-bearing URL appears in any capture; nothing needed redaction.

# Issue 847, composer Upload Files, captured 2026-08-30

Two substrates, because the issue's own claim and this branch's diff are not
the same claim.

* The deployed box, `https://chat-hive.scubed.co`, serving `main`. This is
  where issue 847 was reported and where its symptom had to be re-measured.
* A chat image built from this branch and run locally on
  `http://127.0.0.1:18080`, which is the only place this branch's own change
  is running.

Signed in on the deployed box as `demo-chat-settings-check@hive-e2e.invalid`,
a dedicated automation identity, through the admin one-time-token flow. No
password was set, reset or rotated, and no shared account was used. The
local container runs with `WEBUI_AUTH=false`, so it needs no identity at all.

## Redaction

The OAuth consent hop's URL carries an `authorization_id` query parameter,
which is a single-use handle to a pending authorization. It is not reproduced
here or in any frame: every URL recorded below is either a bare path or ends
before its query string. No frame shows a credential, and no frame shows a
shared account's address.

## Deployed box, `main`, the state issue 847 describes

The composer's More menu, its `Upload Files` entry, clicked by hand rather
than through the composer's file input directly. That distinction is the one
the issue's last comment asked to be closed out: the entry is a separate
control with its own handler, and it had never been exercised.

```
landed on https://chat-hive.scubed.co/
input-menu-button count=1
"Upload Files" entry visible=true
entry class="flex w-full gap-2 items-center px-3 py-1.5 text-sm select-none cursor-pointer hover:bg-gray-50 dark:hover:bg-gray-800/50"
file chooser opened=true
toasts=[]
attachment chip visible=true
file API responses: ["200 POST https://chat-hive.scubed.co/api/v1/files/","200 GET https://chat-hive.scubed.co/api/v1/files/<id>/process/status"]
```

The entry carries no `opacity-50`, so it is not the disabled arm. The chooser
opens, the upload is accepted, the file is processed, and the attachment chip
renders. No message appears at any point.

The other half of the same control, a picker that is opened and then
cancelled, which is what produced the message the issue is named after:

```
landed on https://chat-hive.scubed.co/
chooser opened=true
toasts after cancelling the picker=[]
```

Both arms are quiet. The chat composer's copy of the handler was fixed by
pull request 1375 and that fix is deployed.

## This branch, local image, the state after this change

Same two arms in the composer, plus the knowledge base picker, which is one
of the copies pull request 1375 deferred.

```
landed on http://127.0.0.1:18080/
input-menu-button count=1
"Upload Files" visible=true
chooser opened (cancel case)=true
toasts after cancelling the picker=[]
chooser opened (upload case)=true
attachment chip visible=true
toasts after a real upload=[]
```

```
landed on http://127.0.0.1:18080/workspace/knowledge/create
knowledge base ready
chooser opened=true
toasts after cancelling the knowledge picker=[]
```

Cancelling raises nothing on either surface, and a real selection still
uploads and renders its chip, so the branch did not buy silence by breaking
the control.

## Frames

| File | What it shows |
| --- | --- |
| `01-composer-upload-files-live.png` | The deployed box, the composer after `Upload Files` was clicked in the More menu and a file chosen. The attachment chip is rendered and no message is on screen |
| `02-composer-cancelled-picker-local.png` | This branch, the composer after the same entry was clicked and the picker cancelled. Nothing is raised |
| `03-knowledge-cancelled-picker-local.png` | This branch, a knowledge base after its `Upload files` picker was cancelled. Nothing is raised |

The images are attached to the pull request as a rendered comment through
`scripts/post-pr-visual-proof.sh`, not committed here, because a
`raw.githubusercontent.com` link pinned to this branch stops resolving the
moment the branch is deleted on merge.

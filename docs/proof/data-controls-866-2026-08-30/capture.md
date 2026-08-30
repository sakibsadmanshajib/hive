# Settings > Data Controls, Archive All and Delete All: capture log, 2026-08-30

Issue #866. Branch `fix/866-data-controls-bulk-chat`.

Ten screenshots, posted to the pull request through
`scripts/post-pr-visual-proof.sh`. This file is the text half of the same
capture: the transcript, the row counts either side of each action, and what
the two together prove.

## What it ran against

A container built from this branch with `deploy/docker/Dockerfile.open-webui`
(`docker build -t hive-owui-866:local .`), run on its own throwaway sqlite
database with no volume, so the database is created empty at start and is
destroyed with the container. The frontend in that image is the one this pull
request changes; the backend is the same pinned upstream image and the same
`owui-patches` chain the demo box runs.

Not the deployed box, and deliberately so. The two writes under test are
`POST /api/v1/chats/archive/all` and `DELETE /api/v1/chats/`; the second hard
deletes every chat the caller owns, along with its messages, with no undo. A
capture that proves the delete has to actually perform the delete, which rules
out the shared demo account and every shared fixture account
(`docs/live-test-auth.md`). It also rules out a read-only session against a
deployed environment, since there is nothing to see without the write. So both
identities in this run were created by the run, exist only inside a container
that no longer exists, and hold nothing but the chats seeded below.

Both accounts were created with generated passwords that are not printed here
and not written anywhere: the transcript records only that a password was
generated. No shared account's password was set, reset or rotated.

Environment overrides, all of them to make a standalone container reachable
without the rest of the Hive stack: `ENABLE_SIGNUP=true`,
`ENABLE_LOGIN_FORM=true`, `OAUTH_AUTO_REDIRECT=false`, `DEFAULT_USER_ROLE=user`.
The demo box runs the inverse of the first three, because every account there is
provisioned through the Hive OIDC provider. Nothing in that set touches chat
ownership, the two endpoints, or the `chat.delete` permission, which is left at
its upstream default of on.

## The two identities

* **Neighbour Account**, the first account on the fresh database and therefore
  the instance admin. It is the control: it holds two chats, one of them
  pinned, and does nothing for the whole run.
* **Throwaway Tester**, an ordinary `user`, created through the admin add-user
  route because Open WebUI turns `ui.enable_signup` off in its own database
  once the first account exists. Every action below is taken as this account.

## What is proven, and by which pair of facts

1. **Both controls confirm before they act.** Shot 02 and shot 06 are the
   dialogs, each naming what it is about to do and each offering Cancel. Shot
   03 is Cancel taking effect, and the row count either side of it is
   unchanged: `{"chats":2,"pinned":1,"archived":0}` before and after.
2. **Both controls act.** Archive All moved every row to archived
   (`{"chats":2,"pinned":1,"archived":0}` to `{"chats":0,"pinned":0,"archived":3}`);
   Delete All removed every row, archived ones included
   (`{"chats":2,"pinned":1,"archived":3}` to `{"chats":0,"pinned":0,"archived":0}`).
   The counts come from `/api/v1/chats/list`, `/api/v1/chats/pinned` and
   `/api/v1/chats/all/archived`, read as the acting user, so they are the
   server's state and not the sidebar's opinion of it. `chats` excludes pinned
   rows, which is why two plus one pinned reads as three archived.
3. **Both now say what happened.** Shots 04 and 07 carry "Archived all chats."
   and "Deleted all chats.", which is the half that did not exist before: the
   old handlers discarded the response body and showed nothing at all, success
   or failure.
4. **The pinned section no longer outlives its rows.** Shot 08 is the sidebar
   after Delete All with the settings modal dismissed: no Pinned heading, no
   rows. Before this change Delete All never touched the pinned store, so the
   pinned conversation stayed on screen, named and clickable, pointing at a
   deleted row.
5. **A refused write is reported rather than passing for a success.** Shot 10.
   The response is replaced in the browser with the exact shape the backend
   returns when its model function swallows an exception, HTTP 200 with a
   `false` body, and the surface now says "Failed to delete all chats." The
   chat that existed at that moment survived the refusal
   (`{"chats":1,"pinned":0,"archived":0}` after), which is the point: nothing
   was destroyed and nothing claimed otherwise.
6. **Neither write crossed accounts.** Shot 09 is the neighbour account after
   all of the above, still holding both of its chats with the pinned one still
   pinned (`{"chats":1,"pinned":1,"archived":0}`, unchanged from before the run).
   This is the live half of the scoping claim;
   `scripts/test_owui_bulk_chat_authz.py` is the other half and pins it against
   the patched source with a mutation leg.

## Console noise, named so it is not read as a finding

Every `console.error` in the transcript is one repeated 404, and the container
log names it: `GET /api/v1/hive/credits/balance`. That is the credits banner
reaching for the control plane, which this standalone container does not run
(`HIVE_CONTROL_PLANE_URL` is unset). It is unrelated to Data Controls and
absent on any deployment that has a control plane.

## Transcript

```
# Data Controls capture, 2026-08-30T10:11:39.561Z
base URL: http://localhost:3999 (local container built from this branch, throwaway sqlite)

signed up neighbour-935cc1fe-f7b4-4ca4-892f-4a89154883f3@hive-866.invalid as admin (password generated, not recorded)
created tester-a3bc9043-85bb-48b3-8f3d-d296d43161e0@hive-866.invalid as user (password generated, not recorded)

## Seeded rows
  neighbour before: {"chats":1,"pinned":1,"archived":0}
  tester before: {"chats":2,"pinned":1,"archived":0}

## 1. Before: the tester sidebar, three chats, one of them pinned
  screenshot 01-before-sidebar.png at http://localhost:3999/

## 2. Archive All asks first, and names what it will do
  screenshot 02-archive-all-confirm.png at http://localhost:3999/

## 3. Cancel dismisses it without acting
  screenshot 03-cancel-dismissed.png at http://localhost:3999/
  tester after cancel: {"chats":2,"pinned":1,"archived":0}

## 4. Confirmed: archived, said so, and the sidebar agrees
  screenshot 04-archive-all-done.png at http://localhost:3999/
  tester after archive all: {"chats":0,"pinned":0,"archived":3}
  screenshot 05-archive-sidebar-empty.png at http://localhost:3999/

## 6. Fresh rows, then Delete All asks first
  tester before delete all: {"chats":2,"pinned":1,"archived":3}
  screenshot 06-delete-all-confirm.png at http://localhost:3999/

## 7. Confirmed: deleted, said so, and the pinned section went with it
  screenshot 07-delete-all-done.png at http://localhost:3999/
  tester after delete all: {"chats":0,"pinned":0,"archived":0}
  screenshot 08-delete-sidebar-empty.png at http://localhost:3999/

## 9. The neighbour account is untouched by all of it
  screenshot 09-neighbour-untouched.png at http://localhost:3999/
  neighbour after: {"chats":1,"pinned":1,"archived":0}

## 10. A refused write is now reported, instead of passing for a success
   The server response is replaced in the browser with the exact shape the
   backend returns when its model function swallows an exception: HTTP 200
   with a false body. Before this change nothing was shown at all.
  screenshot 10-refused-write-reported.png at http://localhost:3999/
  tester after refused delete: {"chats":1,"pinned":0,"archived":0}
```

The repeated `[console.error] Failed to load resource: the server responded
with a status of 404 (Not Found)` lines are elided from the transcript above
and explained in the section before it. Nothing else was removed, and no URL in
this run carries a credential in its query string: every navigation is
`http://localhost:3999/`, and the session token was placed in `localStorage`
directly rather than passed through a URL.

## What this capture does not cover

The old behaviour. There is one image build in this run, and it is of the fixed
frontend, so no "before" screenshot exists. The before state is pinned by the
unit tests instead: `vendor/open-webui/src/lib/hive/bulkChatActions.test.ts`
went red against the missing module before the fix and its assertions describe
exactly what the old handlers did not do (report a `false` body, refuse to
navigate on failure, refetch the pinned list).

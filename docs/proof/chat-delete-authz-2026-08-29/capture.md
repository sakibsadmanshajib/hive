# Chat deletion and cross-account denial: capture log, 2026-08-29

Issues #916 and #848. Related: #828 (account deletion), #873 (buglog routing).

Environment: the deployed demo box, `https://chat-hive.scubed.co`, reached
through the demo-box SOCKS proxy at `127.0.0.1:1080`. Chromium 1440x900,
Playwright.

Identities: two independent run-key-scoped fixture accounts, seeded for this
run through `apps/web-console/tests/e2e/support/e2e-fixture-seed.mjs`:

    A  e2e-verified+del916a@hive-e2e.invalid
    B  e2e-verified+del916b@hive-e2e.invalid

Neither is `demo@hive-demo.invalid`. Nothing belonging to the demo account was
read into a session, modified or deleted at any point in this capture. Both
sessions were minted through GoTrue's admin one-time-token flow, the two calls
`live-auth.mjs` makes, and each account then completed the real OAuth consent
hop into the chat instance. No password was read, set, reset or rotated on any
account.

No URL in this capture carries a credential in its query string. The recorded
pages are `https://chat-hive.scubed.co/` with no query string, and the OAuth
consent hop's `authorization_id` is a one-shot value already consumed by the
time of capture; it does not appear in any screenshot.

## What was being tested

`docs/live-test-auth.md` asserted that "there is no chat-delete, task-delete or
account-delete route wired up yet". This capture establishes what is actually
true per surface, and in particular whether a user can delete their own
conversation and be refused another user's.

## 1. Both identities sign in through the real OAuth path

    [signin del916a] url=https://chat-hive.scubed.co/ authsStatus=200 email=e2e-verified+del916a@hive-e2e.invalid
    [signin del916b] url=https://chat-hive.scubed.co/ authsStatus=200 email=e2e-verified+del916b@hive-e2e.invalid

## 2. Cross-account denial

Account A creates a conversation. Account B, signed in on the same shared chat
instance, attempts to read and then delete it.

    [A create]                        status=200
    [A create]                        chatId=e85bb8ac-32f1-4bcb-a5af-2c56060ce571
    [B GET    A's chat]               status=401 {"detail":"We could not find what you're looking for :/"}
    [B DELETE A's chat]               status=404 {"detail":"We could not find what you're looking for :/"}
    [A GET after B's delete attempt]  status=200 survived=true

B is refused on both verbs and A's conversation is intact afterwards. The
handler resolves the row with `get_chat_by_id_and_user_id` and deletes with
`delete_chat_by_id_and_user_id`, so a non-owner's id never matches a row.

## 3. A user deletes their own conversation, through the shipped UI

Two conversations were created so that the after shot distinguishes a deletion
from a sidebar that merely failed to load:

    created "keep-this-conversation-1788027770069"    status=200 id=63f4ff43-ae8c-4260-9784-c119a0e69233
    created "delete-this-conversation-1788027770069"  status=200 id=3788a416-e696-434d-baa5-c152a2b2ea87

    before: doomed row visible=true
    before: keep row visible=true

The row's own menu was opened and Delete clicked, which raises the confirm
dialog "Delete chat? This will delete delete-this-conversation-1788027770069."
with Cancel and Confirm. Confirm was clicked. The request the UI issued:

    DELETE /api/v1/chats/3788a416-e696-434d-baa5-c152a2b2ea87 -> 200

After a full page reload:

    after reload: doomed row gone=true  keep row still present=true

## 4. The deletion is hard, not a hidden flag

Read straight out of the running container's `/data/webui.db` afterwards:

    deleted chat rows:            0
    kept chat rows:               1
    deleted chat_message rows:    0
    chat columns: ['id', 'user_id', 'title', 'created_at', 'updated_at',
                   'share_id', 'archived', 'chat', 'pinned', 'meta',
                   'folder_id', 'tasks', 'summary', 'last_read_at']

The row and its messages are gone. An `archived` column exists and is
untouched by this path, so the product does have a soft option and Delete
deliberately is not it.

## 5. What deletion does not remove

Chat rows live in Open WebUI's own store (`/data/webui.db` in the chat
container). The credit ledger and the audit tables live in Postgres. The delete
path references neither, so no ledger row and no audit event is removed by
deleting a conversation. Confirmed by reading the delete implementation: it
touches `chat`, `chat_message`, `shared_chat` and nulls `automation_run.chat_id`,
and nothing else.

## 6. Surfaces that genuinely have no delete route

    agent tasks   create, list, get, cancel, events, files. No DELETE.
                  apps/control-plane/internal/agenttask/http.go:51-111
    accounts      no product route. sweepStaleFixtureRuns in
                  e2e-fixture-seed.mjs does delete a stale fixture account
                  with the service role, after explicitly unmapping its
                  tenant_billing_accounts row; that unmapping is the fix for
                  #828 (closed), since the account_id foreign key is
                  ON DELETE RESTRICT on purpose.

## 7. Demo-account inventory, read only

Counted through the same read-only database handle, for the cleanup proposal.
Nothing was deleted.

    demo@hive-demo.invalid   24 conversations, 21 of them with no answer
                             earliest 2026-08-23, latest 2026-08-29

Every title is automation text ("Reply with exactly: QAOK", "Count from one to
twelve in words.", "QAPROBE-A", "What is 2+2?", "Say OK"). Five were created on
2026-08-29, the day of this capture, so the litter is still accumulating.

## Cleanup after this capture

Every conversation this run created on its own fixture accounts was deleted
through the same route, which is itself further exercise of it:

    pre-clean DELETE 12072bda "delete-this-conversation-1788027635587" -> 200
    pre-clean DELETE ec689d80 "keep-this-conversation-1788027635587"   -> 200
    pre-clean DELETE e85bb8ac "delete-authz-probe-1788027585985"       -> 200
    pre-clean DELETE 94ec2f10 "delete-authz-probe-1788027562729"       -> 200

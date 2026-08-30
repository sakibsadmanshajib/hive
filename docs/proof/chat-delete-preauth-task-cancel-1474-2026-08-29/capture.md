# Chat delete cancelled another user's in-flight tasks before checking ownership: capture log, 2026-08-29

Issue #1474. Related: #1462 (the review this was found in), #1186 (the admin-arm
flag gate), #947 / #948 / #949 (the admin-exposure family that makes a chat id a
weaker secret than it looks).

Branch `fix/1474-chat-delete-preauth-task-cancel`, head `dcca652e`, PR #1496.

## What could not be captured, and why

**There is no screenshot of the chat interface in this capture, and no live
before/after against the deployed box.** Saying so plainly rather than
substituting something adjacent.

Three separate reasons, each sufficient on its own:

1. **The visible flow is unchanged.** This PR moves a side effect that fires
   before authorisation. The sidebar row menu, the confirm dialog and the
   resulting `DELETE /api/v1/chats/{id}` are byte for byte what they were. An
   interface screenshot would show the same pixels before and after and would be
   evidence of nothing.
2. **The after state does not exist yet on the running stack.** The demo box
   runs the pre-fix image and is read only to this run. The behaviour this PR
   changes will first exist on the box when `deploy-demo-box.yml` rebuilds the
   chat image from this commit.
3. **No live session could be minted.** The audited path is the GoTrue admin
   one-time-token flow in `apps/web-console/tests/e2e/support/live-auth.mjs`,
   which requires `SUPABASE_URL`, `SUPABASE_SERVICE_ROLE_KEY` and
   `SUPABASE_ANON_KEY`. This checkout's environment has none of them populated.
   The forbidden shortcut, writing a password onto a shared account to sign in,
   was not taken and must never be: it broke three concurrent runs on
   2026-08-08 (`docs/live-test-auth.md`). `demo@hive-demo.invalid` was not
   touched at any point, and no account's password was read, set, reset or
   rotated.

What follows instead is the behaviour of the real patched handler, executed.
Not a description of it, and not a structural assertion about where a line sits.

## How the handler is executed without a stack

`scripts/test_owui_chat_delete_task_cancel.py` extracts `delete_chat_by_id` from
the **patched** router source, compiles it and runs it against recording stubs,
then counts the cancellations a call actually performed. It runs the same driver
against two patch chains: without the new patch, and with it. The image builds
only the frontend from `vendor/open-webui` and rewrites `routers/chats.py` inside
the pinned upstream backend image at build time, so the patched source is the
shipped source and the vendored copy is not.

## 1. Pre-fix: the defect, reproduced

Patch chain WITHOUT `apply_chat_delete_task_cancel_1474_patch.py`. This is the
code as it stood, not a paraphrase of it.

```
pre-fix source: patch chain WITHOUT the #1474 patch
  chain: apply_router_authz_family_patch.py, apply_authz_residuals_1191_patch.py
  ok: the pre-fix source carries no # hive (#1474) marker, so this leg really is the code as it stood
  ok: a non-owner's DELETE is refused with 404
  ok: a non-owner's DELETE deletes nothing
  ok: PRE-FIX: a non-owner's refused DELETE cancelled the owner's in-flight tasks (observed 1 cancellation(s)); if this stops reproducing, the post-fix leg below proves nothing
  ok: a caller without the chat.delete permission is refused with 401
  ok: PRE-FIX: a caller refused for lack of the permission still cancelled
  ok: the owner's DELETE succeeds
  ok: the owner's DELETE deletes their row
  ok: the owner's DELETE still cancels their own in-flight tasks, exactly once
  ok: the admin arm answers 404 for a chat that does not exist
  ok: PRE-FIX: the admin arm cancelled before resolving the row
  ok: the admin arm deletes a chat that does resolve
  ok: the admin arm still cancels that chat's in-flight tasks, exactly once
```

A refused non-owner cancelled the owner's in-flight tasks. So did a caller
refused for lack of the `chat.delete` permission, and so did the admin arm for a
chat that did not resolve. The 404 and the 401 were correct throughout, which is
the point: asserting the refusal proves nothing about the side effect in front of
it.

## 2. Post-fix: the full chain the image runs

```
post-fix source: the full chain the image runs
  chain: apply_router_authz_family_patch.py, apply_authz_residuals_1191_patch.py, apply_chat_delete_task_cancel_1474_patch.py
  ok: the #1474 patch applied: 3 # hive (#1474) markers in the patched router (found 3)
  ok: a non-owner's DELETE is refused with 404
  ok: a non-owner's DELETE deletes nothing
  ok: a non-owner's refused DELETE cancels NOTHING (observed 0 cancellation(s))
  ok: a caller without the chat.delete permission is refused with 401
  ok: a caller refused for lack of the permission cancels nothing
  ok: the owner's DELETE succeeds
  ok: the owner's DELETE deletes their row
  ok: the owner's DELETE still cancels their own in-flight tasks, exactly once
  ok: the owner's DELETE cancels BEFORE it deletes, so no task outlives the row it is writing to (observed ['cancel', 'delete'])
  ok: the admin arm answers 404 for a chat that does not exist
  ok: the admin arm cancels nothing when the row does not resolve
  ok: the admin arm deletes a chat that does resolve
  ok: the admin arm still cancels that chat's in-flight tasks, exactly once
  ok: the admin arm also cancels BEFORE it deletes (observed ['cancel', 'delete'])

PASS
```

Zero cancellations for every refused caller. The owner's own cancellation
survives, exactly once, in both arms, and the interleaving is pinned rather than
just the count.

## 3. The test can go red

An exact-literal rewrite whose anchor stops matching upstream does nothing at
all, and that failure looks exactly like success from the outside. So the patch
was replaced with a no-op that prints and returns, and the suite re-run against
it:

```
post-fix source: the full chain the image runs
  chain: apply_router_authz_family_patch.py, apply_authz_residuals_1191_patch.py, apply_chat_delete_task_cancel_1474_patch.py
  FAIL: the #1474 patch applied: 3 # hive (#1474) markers in the patched router (found 0)
  ok: a non-owner's DELETE is refused with 404
  ok: a non-owner's DELETE deletes nothing
  FAIL: a non-owner's refused DELETE cancels NOTHING (observed 1 cancellation(s))
  ok: a caller without the chat.delete permission is refused with 401
  FAIL: a caller refused for lack of the permission cancels nothing
  ok: the owner's DELETE succeeds
  ok: the owner's DELETE deletes their row
  ok: the owner's DELETE still cancels their own in-flight tasks, exactly once
  ok: the admin arm answers 404 for a chat that does not exist
  FAIL: the admin arm cancels nothing when the row does not resolve
  ok: the admin arm deletes a chat that does resolve
  ok: the admin arm still cancels that chat's in-flight tasks, exactly once

FAILED: 4 check(s)
  - the #1474 patch applied: 3 # hive (#1474) markers in the patched router (found 0)
  - a non-owner's refused DELETE cancels NOTHING (observed 1 cancellation(s))
  - a caller refused for lack of the permission cancels nothing
  - the admin arm cancels nothing when the row does not resolve
MUTANT_EXIT=1
```

Four checks red, including the marker read-back and all three cancellation
assertions. A silently inert patch does not pass here.

## 4. The patch applies to the pinned backend image, not only to the vendored copy

Run inside
`ghcr.io/open-webui/open-webui@sha256:9fcea9c6e32ab60b0498f3986c6cdf651ddbe61db48d2213a3d28048ddd673d4`,
applying the real chain in the Dockerfile's order against
`/app/backend/open_webui/routers/chats.py`:

```
apply_router_authz_family_patch: flag-gated 79 unflagged admin bypasses across 11 routers (#1186)
apply_authz_residuals_1191_patch: flag-gated reindex and shared-chat grant skip across 2 routers (#1191)
apply_chat_delete_task_cancel_1474_patch: chat-delete task cancellation moved below the ownership check in both arms (#1474)

# hive (#1474) markers in chats.py:
3
top-level pre-auth cancellations left:
0
```

The non-admin arm as the image now runs it, read back out of that container:

```

        chat = await Chats.get_chat_by_id_and_user_id(id, user.id, db=db)
        if not chat:
            raise HTTPException(
                status_code=status.HTTP_404_NOT_FOUND,
                detail=ERROR_MESSAGES.NOT_FOUND,
            )
        # hive (#1474): the caller is entitled to this chat from here, so
        # cancelling its in-flight tasks is no longer a side effect a
        # refused caller can reach.
        # Reached after the chat.delete permission gate and the scoped lookup.
        await stop_item_tasks(request.app.state.redis, id)
        await Chats.delete_orphan_tags_for_user(chat.meta.get('tags', []), user.id, threshold=1, db=db)
```

`ast.parse` on the result: `chats.py parses`.

## 5. The Dockerfile drift guard goes red if the fix is ever dropped

The two new guard lines were run inside the same container twice, once with the
patch applied and once with it skipped:

```
--- guard with #1474 patch SKIPPED (must fail) ---
guard correctly FAILED
--- with the patch applied ---
DRIFT GUARD LINES PASS
```

Both halves are asserted: three `# hive (#1474)` markers, and zero four-space
indented `await stop_item_tasks(request.app.state.redis, id)` lines, so a rewrite
that inserted the two arm copies and forgot to remove the original is caught too.

## 6. Idempotent, and the #1462 guards stay green

```
apply_chat_delete_task_cancel_1474_patch: chat-delete task cancellation moved below the ownership check in both arms (#1474)
apply_chat_delete_task_cancel_1474_patch: already applied
3
```

```
$ python3 scripts/test_owui_chat_delete_authz.py
...
Caddyfile.owui
  ok: no matcher answers DELETE /api/v1/chats/e85bb8ac-32f1-4bcb-a5af-2c56060ce571 with a 4xx or 5xx before the proxy (found: [])
PASS

$ python3 scripts/test_owui_knowledge_authz.py
ok: knowledge by-id ownership (#1056), retrieval filter and
ok: router family flag-gating (#1186) and residuals (#1191) all enforced
```

## Credentials

No URL in this capture carries a credential in a query string or anywhere else.
No token, key, password or session appears in any line above or in the posted
image, which is a rendering of exactly the transcripts reproduced here. Nothing
required redaction, and nothing was redacted, which is stated so that the absence
of a redaction note is not read as an oversight.

## 7. The ordering is pinned, not just the counts

Adversarial and security review both landed the same finding independently: the
first version of this suite kept `cancelled` and `deleted` as two separate lists,
so their interleaving was unobservable, and a patch that cancelled BELOW the
delete passed every check. That mutant is real, not hypothetical. It matters
because a streaming task that outlives the row delete goes on writing to a chat
that no longer exists, which is the orphaned-request case the upstream comment
this patch removes existed to prevent.

The recorder now keeps one ordered event log, and both owner legs assert
`["cancel", "delete"]`. Re-running the reviewer's mutant, which moves the
cancellation below the delete in both arms while keeping all three markers and
every count identical:

```
post-fix source: the full chain the image runs
  chain: apply_router_authz_family_patch.py, apply_authz_residuals_1191_patch.py, apply_chat_delete_task_cancel_1474_patch.py
  ok: the #1474 patch applied: 3 # hive (#1474) markers in the patched router (found 3)
  ok: a non-owner's DELETE is refused with 404
  ok: a non-owner's DELETE deletes nothing
  ok: a non-owner's refused DELETE cancels NOTHING (observed 0 cancellation(s))
  ok: a caller without the chat.delete permission is refused with 401
  ok: a caller refused for lack of the permission cancels nothing
  ok: the owner's DELETE succeeds
  ok: the owner's DELETE deletes their row
  ok: the owner's DELETE still cancels their own in-flight tasks, exactly once
  FAIL: the owner's DELETE cancels BEFORE it deletes, so no task outlives the row it is writing to (observed ['delete', 'cancel'])
  ok: the admin arm answers 404 for a chat that does not exist
  ok: the admin arm cancels nothing when the row does not resolve
  ok: the admin arm deletes a chat that does resolve
  ok: the admin arm still cancels that chat's in-flight tasks, exactly once
  FAIL: the admin arm also cancels BEFORE it deletes (observed ['delete', 'cancel'])

FAILED: 2 check(s)
  - the owner's DELETE cancels BEFORE it deletes, so no task outlives the row it is writing to (observed ['delete', 'cancel'])
  - the admin arm also cancels BEFORE it deletes (observed ['delete', 'cancel'])
```

Two red, and only the two ordering assertions. Every count-based check stays
green, which is exactly the point: those checks cannot see this defect, and now
something can.

The patch script gained the same property as a build-time backstop, asserted per
arm over that arm's own span rather than "somewhere above", since a whole-handler
count would let the admin arm's cancellation satisfy the non-admin arm's
assertion. That backstop is defensive rather than mutation tested, and is
described here as such; the behavioural assertion above is the one with a
demonstrated mutant.

## 8. The vendored tree is asserted to be the shipped tree

Also from review: everything here patches `vendor/open-webui` and then reasons
about the source the image runs, which holds only while the vendored frontend
version and the pinned backend image tag agree. They are bumped independently by
design, and nothing asserted they agree, so a drift would have left every patch
test green while exercising source the image never runs.

Both scripts now compare `vendor/open-webui/package.json` against the
`FROM ghcr.io/open-webui/open-webui:v<tag>` line in `Dockerfile.open-webui`.
Bumping the vendored version to `0.11.0` in a scratch copy:

```
vendored frontend tree vs pinned backend image
  ok: both the vendored version and the pinned image tag are readable (vendor=0.11.0, pinned=0.10.2)
  FAIL: vendor/open-webui is the same open-webui version as the backend image pinned in Dockerfile.open-webui, so patching the vendored tree really does describe the source the image runs (vendor=0.11.0, pinned=0.10.2)

chat delete task cancellation ordering (issue #1474)
  FAIL: the vendored tree and the pinned backend image are the same open-webui version, so the patched source is the shipped source (vendor=0.11.0, pinned=0.10.2)
```

## 9. What review found that this PR does NOT fix

Two siblings of the same primitive, both verified in the source and both filed
rather than folded in:

* **#1508, `socket/main.py` `ydoc:document:update`.** Cancels tasks for any
  document id that does not begin with `note:`, with no ownership resolution at
  all, reachable by any verified user over the websocket. Wider than #1474,
  which needed a DELETE. Not fixed here because there is no existing ownership
  boundary to move the call below: adding one is a design question about the Yjs
  namespace, with its own blast radius on note collaboration and chat sync.
* **#1511, `main.py` `/api/tasks/chat/{chat_id}` and `/stop`.** Correctly
  ordered, but the admin arm is a bare `user.role != 'admin'` that
  `ENABLE_ADMIN_CHAT_ACCESS` does not gate, because `main.py` is outside the
  #1186 family patch's file set. Needs the administrator role rather than any
  verified session, and on this instance every tenant OWNER holds one.

The PR body's original claim that the two sibling callers were "already correct"
was wrong on both counts and has been corrected rather than quietly dropped.

# ydoc task-keyspace escape: capture log, 2026-08-29

Issue #1508, sibling of #1474 (PR #1496). Branch
`fix/1508-ydoc-task-cancel-authz`.

## The mechanism, which is a namespace collision rather than a missing check

`open_webui.tasks` keys every registered task by an ITEM ID, and that keyspace is
global. A chat completion registers under the bare chat id
(`main.py`, `create_task(..., id=chat_id)`). The Yjs collaboration handler uses
the same registry as a save debounce, keyed on a `document_id` taken straight off
the socket frame, and resolves ownership only for `note:`-prefixed ids.

So a `document_id` that is not `note:`-prefixed, which is the shape of a bare
chat UUID, reached both `stop_item_tasks(REDIS, document_id)` and
`create_task(REDIS, debounced_save(), document_id)` having passed no ownership
resolution at all. The cancellation landed in the chat keyspace.

## What gates exist, stated precisely, because severity depends on it

Two, and neither is an ownership check.

1. **An authenticated socket session.** `SESSION_POOL[sid]` is populated only in
   `connect` and `user-join`, both of which verify a token. The caller is a
   verified user, not an anonymous one.
2. **Room membership.** `sid` must be in `doc_<document_id>`.

The second is not a boundary, because the caller satisfies it themselves:
`ydoc:document:join` applies the identical `note:`-only check and then calls
`sio.enter_room(sid, f'doc_{document_id}')` unconditionally for every other id.
Two frames, no refusal returned, and `except Exception: pass` around the
cancellation means nothing is logged.

**The reachable class is therefore the same as #1474's**, any verified user
holding another user's chat id, not a wider one. What is worse here than in
#1474 is only that it is silent and that it bypasses the `chat.delete`
permission entirely.

## The fix, and why it does not touch the room test

Both task-registry calls are confined to the namespace whose ownership the
handler actually resolves. Entitlement is keyed on the caller's IDENTITY
(`user.get('id')` against `note.user_id`, or an AccessGrants write grant), never
on their connection.

Deliberately NOT fixed by tightening room membership. Room membership is
caller-side connection state, it is precisely what the attacker already
controls, and a guard keyed on the connection rather than on who the caller is
would be the same class of defect wearing a fix's clothing.

Nothing is lost. `note:` is the only namespace any client uses: the single call
site supplying a document id is `NoteEditor.svelte`,
``documentId={`note:${note.id}`}``, and no server-side code produces one. The
debounced save is itself note-only, since `document_save_handler` returns
without acting on any other id. For every id now skipped, the cancellation was
reaching into another subsystem's keyspace and the registration was scheduling a
no-op that merely occupied a slot in it.

## 1. Pre-fix: the defect, reproduced

```
pre-fix source: socket/main.py WITHOUT the #1508 patch
  ok: the pre-fix source carries no # hive (#1508) marker, so this leg really is the code as it stood
  ok: PRE-FIX: a stranger's ydoc update cancelled the victim chat's in-flight tasks (observed 1 cancellation(s) on the chat keyspace); if this stops reproducing, the post-fix leg below proves nothing
  ok: PRE-FIX: it also registered a task under the victim's chat id (observed ['e85bb8ac-32f1-4bcb-a5af-2c56060ce571'])
  ok: an underscore-mangled chat id behaves the same as the plain one (observed 1 cancellation(s))
  ok: the note owner's update still cancels the pending save for their own note, exactly once (observed ['note:3788a416-e696-434d-baa5-c152a2b2ea87'])
  ok: and still schedules the next save (observed ['note:3788a416-e696-434d-baa5-c152a2b2ea87'])
  ok: a stranger's update on someone else's note touches the task registry not at all (cancelled=[], registered=[])
```

## 2. Post-fix

```
post-fix source: with the #1508 patch
  ok: the #1508 patch applied: 2 # hive (#1508) markers (found 2)
  ok: a stranger's ydoc update cancels NOTHING (observed 0)
  ok: and registers nothing in the chat keyspace (observed [])
  ok: an underscore-mangled chat id behaves the same as the plain one (observed 0 cancellation(s))
  ok: the note owner's update still cancels the pending save for their own note, exactly once (observed ['note:3788a416-e696-434d-baa5-c152a2b2ea87'])
  ok: and still schedules the next save (observed ['note:3788a416-e696-434d-baa5-c152a2b2ea87'])
  ok: a stranger's update on someone else's note touches the task registry not at all (cancelled=[], registered=[])

PASS
```

Note collaboration is intact in both legs: the owner still cancels the pending
save and schedules the next one. The leak was not closed by removing the
debounce.

## 3. The test can go red

Patch replaced with a no-op that prints and returns:

```
post-fix source: with the #1508 patch
  FAIL: the #1508 patch applied: 2 # hive (#1508) markers (found 0)
  FAIL: a stranger's ydoc update cancels NOTHING (observed 1)
  FAIL: and registers nothing in the chat keyspace (observed ['e85bb8ac-32f1-4bcb-a5af-2c56060ce571'])
  FAIL: an underscore-mangled chat id behaves the same as the plain one (observed 1 cancellation(s))
  ok: the note owner's update still cancels the pending save for their own note, exactly once (observed ['note:3788a416-e696-434d-baa5-c152a2b2ea87'])
  ok: and still schedules the next save (observed ['note:3788a416-e696-434d-baa5-c152a2b2ea87'])
  ok: a stranger's update on someone else's note touches the task registry not at all (cancelled=[], registered=[])

FAILED: 4 check(s)
  - the #1508 patch applied: 2 # hive (#1508) markers (found 0)
  - a stranger's ydoc update cancels NOTHING (observed 1)
  - and registers nothing in the chat keyspace (observed ['e85bb8ac-32f1-4bcb-a5af-2c56060ce571'])
  - an underscore-mangled chat id behaves the same as the plain one (observed 1 cancellation(s))
```

## 4. The patch applies to the pinned backend image

Run inside
`ghcr.io/open-webui/open-webui@sha256:9fcea9c6e32ab60b0498f3986c6cdf651ddbe61db48d2213a3d28048ddd673d4`:

```
apply_ydoc_task_cancel_1508_patch: ydoc task-registry calls confined to the note namespace whose ownership the handler resolves (#1508)
apply_ydoc_task_cancel_1508_patch: already applied

markers: 2
unguarded cancels left: 0
```

and the emitted handler read back from that container:

```
        # hive (#1508): task ids are one global keyspace, shared with chat
        # completions (create_task(..., id=chat_id) in main.py), so an
        # unvalidated document_id cancelled another user's in-flight
        # completion. Only 'note:' ids have had their ownership resolved,
        # immediately above; every other id has been checked for nothing.
        if document_id.startswith('note:'):
            try:
                await stop_item_tasks(REDIS, document_id)
            except Exception:
                pass
```

## 5. The Dockerfile drift guard goes red if the fix is dropped

```
--- guard with #1508 patch SKIPPED (must fail) ---
guard correctly FAILED
--- with the patch applied ---
DRIFT GUARD LINES PASS
```

Both halves are asserted: two `# hive (#1508)` markers, and zero
eight-space-indented `await stop_item_tasks(REDIS, document_id)` lines, so a
rewrite that added the guarded copy without removing the unguarded one is caught.

## What could not be captured

No screenshot of the chat interface, and no live before-and-after against the
deployed box. Same three reasons as #1474, all still true:

1. **Nothing visible changes.** This is a side effect on a socket frame the
   interface never draws.
2. **The after state does not exist on the running stack yet.** A build-time
   rewrite takes effect when `deploy-demo-box.yml` next rebuilds the chat image,
   not when it merges. The box runs the pre-fix image and is read only here.
3. **No live session could be minted.** The audited path needs `SUPABASE_URL`,
   `SUPABASE_SERVICE_ROLE_KEY` and `SUPABASE_ANON_KEY`, none of which are
   populated in this checkout. No password was read, set, reset or rotated, and
   `demo@hive-demo.invalid` was not touched.

## Credentials

No URL in this capture carries a credential, and no token, key, password or
session appears in any line above or in the posted image, which renders exactly
the transcripts reproduced here. Nothing required redaction and nothing was
redacted, stated so the absence of a redaction note is not read as an oversight.

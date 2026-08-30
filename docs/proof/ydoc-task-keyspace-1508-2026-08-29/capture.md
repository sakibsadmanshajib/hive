# ydoc task-keyspace escape: capture log, 2026-08-29

Issue #1508, sibling of #1474 (PR #1496). Branch
`fix/1508-ydoc-task-cancel-authz`.

Re-verified in full on 2026-08-30 after the adversarial review round posted six
findings on this pull request. Every transcript below was re-run from scratch;
none is carried over. What the review changed is recorded in section 7.

## The mechanism, which is a namespace collision rather than a missing check

`open_webui.tasks` keys every registered task by an ITEM ID, and that keyspace is
global. A chat completion registers under the bare chat id
(`main.py:1638`, `create_task(..., id=chat_id)`). The Yjs collaboration handler
uses the same registry as a save debounce, keyed on a `document_id` taken straight
off the socket frame, and resolves ownership only for `note:`-prefixed ids.

So a `document_id` that is not `note:`-prefixed reached both
`stop_item_tasks(REDIS, document_id)` (`socket/main.py:735`) and
`create_task(REDIS, debounced_save(), document_id)` (`socket/main.py:766`)
having passed no ownership resolution at all. The cancellation landed in the
chat keyspace.

`chat_id` takes three shapes, and all three were reachable: a bare chat UUID, a
`local:<socket_id>` temporary chat, and a `channel:<channel_id>` channel
message. `main.py:1784` and `:1802` and `utils/middleware.py:1512` all branch on
those prefixes, so they are load bearing rather than incidental.

## What gates exist, stated precisely, because severity depends on it

Two, and neither is an ownership check.

1. **An authenticated socket session.** `SESSION_POOL[sid]` is populated only in
   `connect` and `user-join`, both of which verify a token. The caller is a
   verified user, not an anonymous one.
2. **Room membership.** `sid` must be in `doc_<document_id>`.

The second is not a boundary, because the caller satisfies it themselves:
`ydoc:document:join` applies the identical `note:`-only check and then calls
`sio.enter_room(sid, f'doc_{document_id}')` unconditionally at
`socket/main.py:585` for every other id. Two frames, no refusal returned, and
`except Exception: pass` around the cancellation means nothing is logged.

**The reachable class is therefore the same as #1474's**, any verified user
holding another user's chat id, not a wider one. What is worse here than in
#1474 is that no refusal is returned at all, that the `chat.delete` permission
gate is bypassed entirely rather than merely run too late, and that the failure
is silent.

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
  ok: a temporary chat id 'local:sid-victim-socket' reaches the task registry only pre-fix (cancelled=['local:sid-victim-socket'], registered=['local:sid-victim-socket'])
  ok: a channel message id 'channel:5f0b7e3a-1111-2222-3333-444455556666' reaches the task registry only pre-fix (cancelled=['channel:5f0b7e3a-1111-2222-3333-444455556666'], registered=['channel:5f0b7e3a-1111-2222-3333-444455556666'])
  ok: the note owner's update still cancels the pending save for their own note, exactly once (observed ['note:3788a416-e696-434d-baa5-c152a2b2ea87'])
  ok: and still schedules the next save (observed ['note:3788a416-e696-434d-baa5-c152a2b2ea87'])
  ok: a stranger's update on someone else's note touches the task registry not at all (cancelled=[], registered=[])
  ok: no scenario in this leg was swallowed by the handler's own except arm (log.error calls: [])
```

The `local:` and `channel:` legs are new in this round. They matter for the
severity statement as much as for the guard: the pre-fix code cancelled
temporary-chat and channel completions too, not only chats holding a bare
UUID.

## 2. Post-fix

```
post-fix source: with the #1508 patch
  ok: the #1508 patch applied: 2 # hive (#1508) markers (found 2)
  ok: a stranger's ydoc update cancels NOTHING (observed 0)
  ok: and registers nothing in the chat keyspace (observed [])
  ok: an underscore-mangled chat id behaves the same as the plain one (observed 0 cancellation(s))
  ok: a temporary chat id 'local:sid-victim-socket' reaches the task registry only pre-fix (cancelled=[], registered=[])
  ok: a channel message id 'channel:5f0b7e3a-1111-2222-3333-444455556666' reaches the task registry only pre-fix (cancelled=[], registered=[])
  ok: the note owner's update still cancels the pending save for their own note, exactly once (observed ['note:3788a416-e696-434d-baa5-c152a2b2ea87'])
  ok: and still schedules the next save (observed ['note:3788a416-e696-434d-baa5-c152a2b2ea87'])
  ok: a stranger's update on someone else's note touches the task registry not at all (cancelled=[], registered=[])
  ok: no scenario in this leg was swallowed by the handler's own except arm (log.error calls: [])
PASS
```

Note collaboration is intact in both legs: the owner still cancels the
pending save and schedules the next one. The leak was not closed by removing
the debounce.

## 3. The test can go red

Patch replaced with a no-op that prints and returns:

```
post-fix source: with the #1508 patch
  FAIL: the #1508 patch applied: 2 # hive (#1508) markers (found 0)
  FAIL: a stranger's ydoc update cancels NOTHING (observed 1)
  FAIL: and registers nothing in the chat keyspace (observed ['e85bb8ac-32f1-4bcb-a5af-2c56060ce571'])
  FAIL: an underscore-mangled chat id behaves the same as the plain one (observed 1 cancellation(s))
  FAIL: a temporary chat id 'local:sid-victim-socket' reaches the task registry only pre-fix (cancelled=['local:sid-victim-socket'], registered=['local:sid-victim-socket'])
  FAIL: a channel message id 'channel:5f0b7e3a-1111-2222-3333-444455556666' reaches the task registry only pre-fix (cancelled=['channel:5f0b7e3a-1111-2222-3333-444455556666'], registered=['channel:5f0b7e3a-1111-2222-3333-444455556666'])
  ok: the note owner's update still cancels the pending save for their own note, exactly once (observed ['note:3788a416-e696-434d-baa5-c152a2b2ea87'])
  ok: and still schedules the next save (observed ['note:3788a416-e696-434d-baa5-c152a2b2ea87'])
  ok: a stranger's update on someone else's note touches the task registry not at all (cancelled=[], registered=[])
  ok: no scenario in this leg was swallowed by the handler's own except arm (log.error calls: [])
FAILED: 6 check(s)
  - the #1508 patch applied: 2 # hive (#1508) markers (found 0)
  - a stranger's ydoc update cancels NOTHING (observed 1)
  - and registers nothing in the chat keyspace (observed ['e85bb8ac-32f1-4bcb-a5af-2c56060ce571'])
  - an underscore-mangled chat id behaves the same as the plain one (observed 1 cancellation(s))
  - a temporary chat id 'local:sid-victim-socket' reaches the task registry only pre-fix (cancelled=['local:sid-victim-socket'], registered=['local:sid-victim-socket'])
  - a channel message id 'channel:5f0b7e3a-1111-2222-3333-444455556666' reaches the task registry only pre-fix (cancelled=['channel:5f0b7e3a-1111-2222-3333-444455556666'], registered=['channel:5f0b7e3a-1111-2222-3333-444455556666'])
```

## 4. Two more mutants, both from the review round

### 4a. A guard keyed on a colon instead of the note prefix

The mutant the review proposed: `if ':' in document_id:` in both `EDITS`
entries, nothing else changed. It is semantically wrong, because
`local:<socket_id>` and `channel:<channel_id>` chat ids carry a colon, so it
leaves every temporary chat and every channel completion cancellable.

The patch's own AST postcondition refuses to emit it, so the build fails rather
than shipping it:

```
  File "/w/deploy/docker/owui-patches/apply_ydoc_task_cancel_1508_patch.py", line 245, in main
    assert name in guarded, (
           ^^^^^^^^^^^^^^^
AssertionError: `stop_item_tasks` is not inside a document_id.startswith('note:') branch in yjs_document_update, so it can still act on an id whose ownership this handler never resolved
```

The review measured this mutant against commit `6974c832`, before the AST
postcondition existed, and there it passed the whole suite silently. It is
caught now. The suite's own scenarios were still blind to those two id shapes,
which is why they were added in section 1 regardless: the postcondition is one
net, and the observed behaviour should be the other.

### 4b. An unrelated early exception, swallowed by the handler itself

The real `yjs_document_update` wraps its whole body in
`except Exception as e: log.error(...)`. With `log.error` a no-op, any exception
raised for a reason unrelated to the fix produces exactly the observable state a
working guard produces: `cancelled == []` and `registered == []`.

`normalize_document_id` made to raise, with the real patch otherwise in place:

```
post-fix source: with the #1508 patch
  ok: the #1508 patch applied: 2 # hive (#1508) markers (found 2)
  ok: a stranger's ydoc update cancels NOTHING (observed 0)
  ok: and registers nothing in the chat keyspace (observed [])
  ok: an underscore-mangled chat id behaves the same as the plain one (observed 0 cancellation(s))
  ok: a temporary chat id 'local:sid-victim-socket' reaches the task registry only pre-fix (cancelled=[], registered=[])
  ok: a channel message id 'channel:5f0b7e3a-1111-2222-3333-444455556666' reaches the task registry only pre-fix (cancelled=[], registered=[])
  FAIL: the note owner's update still cancels the pending save for their own note, exactly once (observed [])
  FAIL: and still schedules the next save (observed [])
  ok: a stranger's update on someone else's note touches the task registry not at all (cancelled=[], registered=[])
```

Every one of the six leak assertions reads `ok` while the handler is crashing on
every single frame. That is the false green the review named. The new assertion
is the line that goes red on it:

```
  FAIL: no scenario in this leg was swallowed by the handler's own except arm (log.error calls: ['Error in yjs_document_update: MUTANT: an unrelated early failure', 'Error in yjs_document_update: MUTANT: an unrelated early failure', 'Error in yjs_document_update: MUTANT: an unrelated early failure', 'Error in yjs_document_update: MUTANT: an unrelated early failure', 'Error in yjs_document_update: MUTANT: an unrelated early failure', 'Error in yjs_document_update: MUTANT: an unrelated early failure'])
```

## 5. The patch applies to the pinned backend image

Run inside
`ghcr.io/open-webui/open-webui@sha256:9fcea9c6e32ab60b0498f3986c6cdf651ddbe61db48d2213a3d28048ddd673d4`,
not against the vendored copy, and the image reports its own version so the
substrate is named rather than assumed:

```
=== image identity ===
	"version": "0.10.2",
=== apply ===
apply_ydoc_task_cancel_1508_patch: ydoc task-registry calls confined to the note namespace whose ownership the handler resolves (#1508)
apply_ydoc_task_cancel_1508_patch: already applied
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

## 6. The Dockerfile drift guard goes red if the fix is dropped

Both halves of the guard run inside the same pinned image, before and after the
patch:

```
=== BEFORE: drift guard on the unpatched image (must FAIL) ===
guard correctly FAILED before the patch
  markers before: 0
  unguarded 12-space calls before: 1
=== AFTER: drift guard on the patched image (must PASS) ===
DRIFT GUARD LINES PASS
  markers after: 2
  unguarded 12-space calls after: 0
```

The second half was measured wrong in the first version of this branch and is
the whole reason for section 7. The pattern anchored the call at eight spaces of
indentation; in the shipped image it sits at twelve, inside `try:` inside the
handler. So it returned 0 before the patch and 0 after it, which is a check that
cannot fail, backing a claim this log made anyway.

Now it is one, then zero. And the add-without-remove rewrite the claim is about
is genuinely caught. A copy of the guarded form inserted while the unguarded
call is left in place:

```
markers: 2
unguarded 12-space calls: 1
guard correctly FAILED on the add-without-remove mutant
```

Two markers, and it still fails. Under the eight-space pattern it passed.

## 7. What the review round changed

Six findings were posted on this pull request. One was already fixed by the
second commit before it landed; the other five are addressed here.

| Finding | Verdict | Where it is fixed |
| --- | --- | --- |
| `str.rindex` postcondition can never be False | Real, already fixed by commit `43a5481` before the comment landed; the text search was replaced by the AST postcondition in section 4a | no change needed |
| The handler's own `except` swallows unrelated failures, so a crash reads as a clean pass | Real, reproduced in section 4b | `Recorder.errors`, and one assertion per leg |
| `SESSION_POOL` hardcoded to two literal sids, so a third sid would early-return silently and read as a pass | Real, structural | `SESSION_POOL_FOR`, and `emit()` refuses a sid the pool does not know |
| `compile_handler` has no return type hint | Real | annotated |
| The drift-guard grep is anchored at the wrong indent and can never go red | Real, measured in section 6 | eight spaces widened to twelve |
| The attacker id set misses `local:` and `channel:` chat ids | Real as a coverage gap; the mutant it demonstrated is now caught by the AST postcondition, but the scenarios were blind to those shapes | two scenarios added, and they observe the pre-fix leak too |

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
session appears in any line above. Nothing required redaction and nothing was
redacted, stated so the absence of a redaction note is not read as an oversight.

An earlier version of this paragraph, and of the pull request body, referred to
"the posted image". No image was ever posted, on this pull request or on its
sibling. The reference is removed rather than made true: a picture of a terminal
transcript is not visual proof of anything, it is an unrelated frame substituted
for an after state that does not exist yet, which is the substitution the
visual-proof rule exists to forbid. The transcripts are here, in a file
`npm run lint:proof-tokens` actually scans.

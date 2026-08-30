"""Build-time fix for the ydoc task-keyspace escape (issue #1508).

Sibling of #1474, found in the security review of PR #1496. Same primitive,
different door, and a mechanism worth stating exactly because it is not a
missing check so much as two subsystems sharing one namespace.

THE COLLISION
-------------
`open_webui.tasks` keys every registered task by an ITEM ID. A chat completion
registers under the bare chat id (`main.py`, `create_task(..., id=chat_id)`),
and `stop_item_tasks(redis, item_id)` cancels everything registered under the id
it is handed. It resolves no ownership of its own.

The Yjs collaboration handler uses that same registry as a debounce: on each
`ydoc:document:update` it cancels the pending save for the document and
schedules a new one, keyed by `document_id`. And `document_id` comes straight
off the socket frame:

    document_id = normalize_document_id(data['document_id'])   # client supplied
    ... room membership check ...
    ... user = SESSION_POOL.get(sid) ...
    if document_id.startswith('note:'):
        ... the ONLY ownership resolution in the handler ...
    try:
        await stop_item_tasks(REDIS, document_id)     # every id, note or not
    except Exception:
        pass
    ...
    if data.get('data'):
        await create_task(REDIS, debounced_save(), document_id)   # same keyspace

So a `document_id` that is not `note:`-prefixed, which is exactly the shape of a
bare chat UUID, reaches both calls having passed no ownership resolution. The
cancellation lands in the CHAT task keyspace and stops the victim's in-flight
completion, title generation and tag generation.

WHAT GATES DO EXIST, stated precisely, because the severity depends on it
------------------------------------------------------------------------
Two, and neither is an ownership check:

  1. An authenticated socket session. `SESSION_POOL[sid]` is populated only in
     `connect` and `user-join`, both of which verify a token. So the caller is a
     verified user, not an anonymous one.
  2. Room membership: `sid` must be in `doc_<document_id>`.

The second is not a boundary, because it is self-satisfiable.
`ydoc:document:join` applies the identical `note:`-only check and then calls
`sio.enter_room(sid, f'doc_{document_id}')` unconditionally, so a caller joins
the room for any non-`note:` id unchallenged and then satisfies the membership
test in `update`. Two frames, no refusal, and `except Exception: pass` means
nothing is logged.

The reachable class is therefore the same as #1474's: any verified user holding
another user's chat id. Not narrower, and not wider either.

THE FIX
-------
Confine both task-registry calls to the namespace whose ownership this handler
actually resolves. `note:` ids have been checked, immediately above, for note
ownership or an AccessGrants write grant; every other id has been checked for
nothing.

Entitlement here is a property of the caller's IDENTITY (`user.get('id')`
against `note.user_id`, or a grant), never of their connection. Deliberately not
fixed by tightening the room-membership test: room membership is caller-side
connection state, it is exactly what the attacker already controls, and a guard
that keys on the connection rather than on who the caller is would be the same
class of defect wearing a fix's clothing.

WHY THIS LOSES NOTHING
----------------------
`note:` is the only namespace any client ever uses. The single call site that
supplies a document id is `NoteEditor.svelte`, `documentId={`note:${note.id}`}`,
and no server-side code produces a document id at all. And the debounced save
these calls exist to manage is itself note-only: `document_save_handler` returns
without doing anything for a non-`note:` id. So for every id this change now
skips, the cancellation was reaching into another subsystem's keyspace and the
registration was scheduling a no-op that merely occupied a slot in it.

Not a vendored edit, because a vendored edit would ship nothing:
Dockerfile.open-webui builds only the frontend from vendor/open-webui and keeps
the pinned upstream image for the Python backend, then rewrites it there at
build time. socket/main.py is touched by no other patch in this directory.

Fail-loud posture, identical to the sibling authz patches: exact-literal anchors
with expected occurrence counts, ast.parse after every rewrite, marker totals
asserted at the end and again by the Dockerfile drift guard, idempotent
early-exit once applied. HIVE_OWUI_BACKEND_DIR overrides the target directory
for scripts/test_owui_ydoc_task_cancel.py.
"""

import ast
import os
import pathlib

BACKEND = pathlib.Path(
    os.environ.get(
        "HIVE_OWUI_BACKEND_DIR",
        "/app/backend/open_webui",
    )
)

MARKER = "# hive (#1508)"

TARGET = "socket/main.py"

# Each entry: (file, old, new, expected_pre_count). Each rewrite inserts exactly
# one MARKER, so the per-file total is socket/main.py 2. Anchors verified unique
# against the vendored source.
EDITS = [
    # 1. The cancellation. This is the one that reaches another subsystem.
    (
        TARGET,
        "        try:\n"
        "            await stop_item_tasks(REDIS, document_id)\n"
        "        except Exception:\n"
        "            pass\n",
        f"        {MARKER}: task ids are one global keyspace, shared with chat\n"
        "        # completions (create_task(..., id=chat_id) in main.py), so an\n"
        "        # unvalidated document_id cancelled another user's in-flight\n"
        "        # completion. Only 'note:' ids have had their ownership resolved,\n"
        "        # immediately above; every other id has been checked for nothing.\n"
        "        if document_id.startswith('note:'):\n"
        "            try:\n"
        "                await stop_item_tasks(REDIS, document_id)\n"
        "            except Exception:\n"
        "                pass\n",
        1,
    ),
    # 2. The registration, which writes into the same keyspace with the same id.
    #    Fixing only the cancellation would leave the handler still able to plant
    #    a task under another user's chat id.
    (
        TARGET,
        "        if data.get('data'):\n"
        "            await create_task(REDIS, debounced_save(), document_id)\n",
        f"        {MARKER}: same keyspace, same unvalidated id. Nothing is lost by\n"
        "        # confining it: document_save_handler returns without acting on\n"
        "        # any id that is not 'note:'-prefixed, so for every id this now\n"
        "        # skips, the task registered was a no-op occupying a slot in the\n"
        "        # chat task keyspace.\n"
        "        if document_id.startswith('note:') and data.get('data'):\n"
        "            await create_task(REDIS, debounced_save(), document_id)\n",
        1,
    ),
]

EXPECTED_MARKERS = {TARGET: 2}


def is_note_guard(node) -> bool:
    """Exactly `document_id.startswith('note:')`, and nothing that resembles it."""
    return (
        isinstance(node, ast.Call)
        and isinstance(node.func, ast.Attribute)
        and node.func.attr == "startswith"
        and isinstance(node.func.value, ast.Name)
        and node.func.value.id == "document_id"
        and len(node.args) == 1
        and isinstance(node.args[0], ast.Constant)
        and node.args[0].value == "note:"
    )


def main():
    already = all(
        (BACKEND / f).read_text().count(MARKER) == n
        for f, n in EXPECTED_MARKERS.items()
    )
    if already:
        print("apply_ydoc_task_cancel_1508_patch: already applied")
        return

    for filename, old, new, expected in EDITS:
        target = BACKEND / filename
        text = target.read_text()
        n = text.count(old)
        assert n == expected, (
            f"{filename}: anchor found {n} times, expected {expected}; "
            f"upstream open-webui source shifted, patch needs updating. "
            f"Anchor head: {old[:90]!r}"
        )
        patched = text.replace(old, new)
        ast.parse(patched)  # fails the build if a rewrite produced invalid Python
        target.write_text(patched)

    # Neither call may remain reachable for an id whose ownership was never
    # resolved. Counting markers is not enough on its own: a rewrite that added
    # the guarded copies without removing the unguarded ones would still count
    # two. Sliced by AST line span rather than by searching for the next
    # decorator, so an upstream insertion between handlers cannot silently widen
    # what is being inspected.
    final = (BACKEND / TARGET).read_text()
    handler = next(
        (
            node
            for node in ast.walk(ast.parse(final))
            if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef))
            and node.name == "yjs_document_update"
        ),
        None,
    )
    assert handler is not None, "yjs_document_update not found after patching"
    lines = final.splitlines(keepends=True)
    body = "".join(lines[handler.lineno - 1 : handler.end_lineno])

    for call in (
        "await stop_item_tasks(REDIS, document_id)",
        "await create_task(REDIS, debounced_save(), document_id)",
    ):
        assert body.count(call) == 1, (
            f"expected exactly one `{call}` in yjs_document_update after "
            f"patching, found {body.count(call)}"
        )

    # Each call must sit INSIDE a `document_id.startswith('note:')` branch, and
    # this is asked of the AST rather than of the text. A string search for the
    # guard above the call cannot distinguish "inside the branch" from "after
    # the branch has closed", which is the one arrangement that would reopen the
    # hole while keeping both the marker count and the call count correct.
    guarded = set()
    for node in ast.walk(handler):
        if not isinstance(node, ast.If):
            continue
        # Both shapes the patch produces: a bare
        # `document_id.startswith('note:')` for the cancellation, and
        # `document_id.startswith('note:') and data.get('data')` for the
        # registration. An `or` is deliberately NOT accepted: it would widen the
        # branch back open, which is the inversion this check exists to catch.
        test = node.test
        conjuncts = (
            list(test.values)
            if isinstance(test, ast.BoolOp) and isinstance(test.op, ast.And)
            else [test]
        )
        if not any(is_note_guard(c) for c in conjuncts):
            continue
        for sub in ast.walk(ast.Module(body=list(node.body), type_ignores=[])):
            if isinstance(sub, ast.Call) and isinstance(sub.func, ast.Attribute):
                guarded.add(sub.func.attr)
            elif isinstance(sub, ast.Call) and isinstance(sub.func, ast.Name):
                guarded.add(sub.func.id)

    for name in ("stop_item_tasks", "create_task"):
        assert name in guarded, (
            f"`{name}` is not inside a document_id.startswith('note:') branch in "
            "yjs_document_update, so it can still act on an id whose ownership "
            "this handler never resolved"
        )

    for filename, expected in EXPECTED_MARKERS.items():
        count = (BACKEND / filename).read_text().count(MARKER)
        assert count == expected, (
            f"{filename}: {count} markers after patching, expected {expected}"
        )
    print(
        "apply_ydoc_task_cancel_1508_patch: ydoc task-registry calls confined to "
        "the note namespace whose ownership the handler resolves (#1508)"
    )


if __name__ == "__main__":
    main()

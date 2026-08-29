"""Build-time fix for the pre-authorisation task cancellation on chat delete (issue #1474).

`DELETE /api/v1/chats/{id}` called `stop_item_tasks(request.app.state.redis, id)`
as the handler's FIRST statement, above the role split and above every ownership
check. That function takes an item id and a Redis handle and cancels every task
registered against that id; it performs no ownership check of its own. So a
verified user holding another user's chat id cancelled that user's in-flight
completion, title generation and tag generation by issuing a DELETE that was then
refused with a 404. The refusal was real and the chat row survived, but the side
effect had already fired, and a 404 that has cancelled the victim's streaming
response is not a denial.

Chat ids are UUIDv4 and so not guessable, but they are not secret either: they
appear in share links and in URLs, on a shared chat instance with an open
admin-exposure family (#947, #948, #949).

This moves the call into each arm of the role split, in both cases immediately
after that arm's 404, which is the first point at which the caller has been
established as entitled to delete this chat:

  admin arm      lookup, 404, THEN cancel, then delete
  non-admin arm  permission gate, scoped lookup, 404, THEN cancel, then delete

Both arms are treated deliberately. docker-compose.yml sets
ENABLE_ADMIN_CHAT_ACCESS to "false", so on this deployment every caller including
an administrator takes the non-admin arm and only the second edit ever executes.
The first is not therefore dead code: it is what keeps the boundary correct if
that flag is ever turned on, and a fix that holds only because an unrelated flag
happens to be off is not a fix, it is a coincidence.

The cancellation still precedes the delete in both arms, so an owner deleting
their own chat cancels their own in-flight tasks exactly as before. Closing the
leak by removing the cancellation would have traded one defect for another.

Not a vendored edit, because a vendored edit would ship nothing:
Dockerfile.open-webui builds only the frontend from vendor/open-webui and keeps
the pinned upstream image for the Python backend, then rewrites routers/chats.py
inside that image at build time.

Fail-loud posture, identical to the sibling authz patches: exact-literal anchors
with expected occurrence counts, ast.parse after every rewrite, marker totals
asserted at the end and again by the Dockerfile drift guard, idempotent
early-exit once applied. Runs AFTER apply_router_authz_family_patch.py, which
rewrites this handler's `if user.role == 'admin':` line (#1186); no anchor here
overlaps that line. HIVE_OWUI_ROUTERS_DIR overrides the target directory for
scripts/test_owui_chat_delete_task_cancel.py.
"""

import ast
import os
import pathlib

ROUTERS = pathlib.Path(
    os.environ.get(
        "HIVE_OWUI_ROUTERS_DIR",
        "/app/backend/open_webui/routers",
    )
)

MARKER = "# hive (#1474)"

CANCEL_CALL = "await stop_item_tasks(request.app.state.redis, id)"

# The tail both arms share: the 404 that refuses a caller the lookup did not
# resolve a row for, and the orphan-tag sweep that follows it. Each arm's anchor
# is this tail prefixed with that arm's own lookup line, which is what makes the
# two anchors unique. Verified: the tail alone occurs twice, each full anchor
# once.
ARM_TAIL = (
    "        if not chat:\n"
    "            raise HTTPException(\n"
    "                status_code=status.HTTP_404_NOT_FOUND,\n"
    "                detail=ERROR_MESSAGES.NOT_FOUND,\n"
    "            )\n"
)
ORPHAN_TAGS = (
    "        await Chats.delete_orphan_tags_for_user("
    "chat.meta.get('tags', []), user.id, threshold=1, db=db)\n"
)


def _cancel_after_404(lookup: str, note: str) -> tuple[str, str]:
    """Anchor and replacement for one arm: the cancellation lands below the 404."""
    old = lookup + ARM_TAIL + ORPHAN_TAGS
    new = (
        lookup
        + ARM_TAIL
        + f"        {MARKER}: the caller is entitled to this chat from here, so\n"
        "        # cancelling its in-flight tasks is no longer a side effect a\n"
        "        # refused caller can reach.\n"
        f"        # {note}\n"
        f"        {CANCEL_CALL}\n"
        + ORPHAN_TAGS
    )
    return old, new


# Each entry: (file, old, new, expected_pre_count). Each rewrite inserts exactly
# one MARKER, so the per-file total is chats.py 3.
EDITS = [
    # 1. The pre-authorisation call itself.
    (
        "chats.py",
        "    # Cancel any in-flight LLM tasks (streaming, title/tags generation)\n"
        "    # before deleting the chat to prevent orphaned requests.\n"
        f"    {CANCEL_CALL}\n",
        f"    {MARKER}: the cancellation used to run HERE, as this handler's first\n"
        "    # statement, above the role split and above every ownership check, so\n"
        "    # any verified user holding another user's chat id could cancel that\n"
        "    # user's in-flight completion and title generation with a DELETE they\n"
        "    # were then refused with a 404. It now runs inside each arm, below the\n"
        "    # point at which the caller has been established as entitled to delete\n"
        "    # this chat, and still ahead of the delete itself.\n",
        1,
    ),
    # 2. Admin arm. Unreachable while ENABLE_ADMIN_CHAT_ACCESS is "false", which
    #    is what compose sets; patched anyway, so the boundary does not depend on
    #    that flag staying off.
    (
        "chats.py",
        *_cancel_after_404(
            "        chat = await Chats.get_chat_by_id(id, db=db)\n",
            "Reached only when ENABLE_ADMIN_CHAT_ACCESS is on (#1186).",
        ),
        1,
    ),
    # 3. Non-admin arm, which every caller on this deployment takes.
    (
        "chats.py",
        *_cancel_after_404(
            "        chat = await Chats.get_chat_by_id_and_user_id(id, user.id, db=db)\n",
            "Reached after the chat.delete permission gate and the scoped lookup.",
        ),
        1,
    ),
]

EXPECTED_MARKERS = {
    "chats.py": 3,
}


def main():
    already = all(
        (ROUTERS / f).read_text().count(MARKER) == n
        for f, n in EXPECTED_MARKERS.items()
    )
    if already:
        print("apply_chat_delete_task_cancel_1474_patch: already applied")
        return

    for filename, old, new, expected in EDITS:
        target = ROUTERS / filename
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

    # The handler must now reach the cancellation only from inside the two arms.
    # Counting the call is not enough on its own: a rewrite that inserted both
    # copies and failed to remove the original would still count three markers.
    final = (ROUTERS / "chats.py").read_text()
    handler_start = final.index("async def delete_chat_by_id(")
    handler_end = final.index("\n@router.", handler_start)
    handler = final[handler_start:handler_end]
    assert handler.count(CANCEL_CALL) == 2, (
        "delete_chat_by_id should cancel from exactly its two arms after patching, "
        f"found {handler.count(CANCEL_CALL)} call(s)"
    )
    # Anchored on the line start, not merely on the four-space prefix: an
    # eight-space arm-level call CONTAINS the four-space pattern as a substring,
    # so a bare `in` test reports the top-level call still present when it is
    # gone. It did, on the first run of this patch.
    assert f"\n    {CANCEL_CALL}\n" not in handler, (
        "delete_chat_by_id still cancels at handler top level (four-space indent), "
        "so the pre-authorisation call was not removed"
    )

    for filename, expected in EXPECTED_MARKERS.items():
        count = (ROUTERS / filename).read_text().count(MARKER)
        assert count == expected, (
            f"{filename}: {count} markers after patching, expected {expected}"
        )
    print(
        "apply_chat_delete_task_cancel_1474_patch: chat-delete task cancellation "
        "moved below the ownership check in both arms (#1474)"
    )


if __name__ == "__main__":
    main()

"""Flag-gate the bare admin bypasses on chat ownership in main.py (issue #1511).

Third sibling from the security review of PR #1496, after #1474 (fixed) and
#1508. Unlike those two this is NOT a pre-authorisation side effect: every site
below resolves ownership before it acts. What is wrong is the WIDTH of the
predicate.

THE FAMILY THIS BELONGS TO
--------------------------
`apply_router_authz_family_patch.py` exists to close exactly this shape for
issue #1186: a gate that short-circuits on bare `user.role == 'admin'` without
consulting the flag that says whether admins may cross tenant boundaries on this
deployment. chats.py uses its own upstream flag, ENABLE_ADMIN_CHAT_ACCESS, and
docker-compose.yml sets it to "false".

`main.py` was never in that patch's scope. Its EXPECTED_MARKERS covers eleven
modules under routers/ and no top-level module, so these sites were not
deliberately excluded, they were simply never looked at. Five chat-ownership
decisions in main.py still read `user.role != 'admin'` with no flag term:

  * /api/tasks/chat/{chat_id}        the socket-scoped arm and the chat arm
  * /api/tasks/chat/{chat_id}/stop   the socket-scoped arm and the chat arm
  * the existing-chat ownership check on the chat-completions path

The last one is included deliberately even though issue #1511 names only the
task endpoints. It is the same predicate, in the same file, gated by the same
flag, reverted by the same patch, and reachable by the same caller; it is also
the most consequential of the five, since it admits a WRITE into another user's
conversation rather than a read or a cancellation. Shipping four of five and
leaving that one would be the "patched only the path the ticket names" error
that this review round already caught once on #1474's siblings.

WHO CAN ACTUALLY REACH THIS, CORRECTED
--------------------------------------
The issue as filed said "on this instance every tenant OWNER holds an
administrator session (#748, #948)". That was true once and is now FALSE, and
the correction matters more than the fix.

`owui-patches/tenant_role_from_db.py` resolves this login's Open WebUI role from
Hive's own Postgres, and its whole point is that it no longer maps a tenant
OWNER onto 'admin'. A login is 'admin' only when it owns an account with
`accounts.is_platform_admin = true`, the same predicate the control plane uses
for its own platform-admin surfaces; an ordinary tenant OWNER resolves to
'user', and an unknown email is left at DEFAULT_USER_ROLE, which is "pending" on
this deployment. The file records the live audit that forced that change: a
legitimately provisioned tenant OWNER had received admin and read another
tenant's chat titles and uploaded file.

So the reachable class here is Hive platform staff, not customers. That is a
much smaller and already-trusted set, and it is why this ships behind #1474 and
#1508 rather than beside them.

It is still worth fixing, for one specific reason rather than on principle:
setting ENABLE_ADMIN_CHAT_ACCESS to "false" is this deployment's statement that
even a platform admin does not get cross-tenant chat access through the product
surface. These five sites ignore that statement, so the flag does not mean what
the compose file says it means, and the next person to reason from it will be
wrong in a direction that favours access.

Not a vendored edit, because a vendored edit would ship nothing:
Dockerfile.open-webui builds only the frontend from vendor/open-webui and keeps
the pinned upstream image for the Python backend. ENABLE_ADMIN_CHAT_ACCESS is
already imported in main.py, so no import edit is needed.

Fail-loud posture, identical to the sibling authz patches: exact-literal anchors
with expected occurrence counts, ast.parse after every rewrite, marker totals
asserted at the end and again by the Dockerfile drift guard, idempotent
early-exit once applied. HIVE_OWUI_BACKEND_DIR overrides the target directory
for scripts/test_owui_main_admin_flag.py.
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

MARKER = "# hive (#1511)"
TARGET = "main.py"

# The predicate the #1186 family patch uses for chats.py, reused verbatim so the
# two cannot drift into meaning different things.
FLAGGED = "(user.role == 'admin' and ENABLE_ADMIN_CHAT_ACCESS)"

# Each entry: (file, old, new, expected_pre_count). Every rewrite inserts one
# MARKER per occurrence rewritten, so the per-file total is main.py 5: two from
# the socket-scoped arm (which appears once in each of the two task endpoints),
# and one each from the two chat arms and the completions ownership check.
EDITS = [
    # 1. The socket-scoped arm, in BOTH task endpoints.
    (
        TARGET,
        "        if owner_id != user.id and user.role != 'admin':\n",
        f"        {MARKER}: bare admin role is not enough; the same flag the\n"
        "        # #1186 family patch uses for chats.py, which compose sets false\n"
        "        if owner_id != user.id and not " + FLAGGED + ":\n",
        2,
    ),
    # 2. GET /api/tasks/chat/{chat_id}: hands another tenant's task ids to the
    #    caller, which are also the ids POST /api/tasks/stop/{task_id} consumes.
    (
        TARGET,
        "        chat = await Chats.get_chat_by_id(chat_id)\n"
        "        if chat is None or (chat.user_id != user.id and user.role != 'admin'):\n"
        "            return {'task_ids': []}\n",
        "        chat = await Chats.get_chat_by_id(chat_id)\n"
        f"        {MARKER}\n"
        "        if chat is None or (\n"
        "            chat.user_id != user.id and not " + FLAGGED + "\n"
        "        ):\n"
        "            return {'task_ids': []}\n",
        1,
    ),
    # 3. POST /api/tasks/chat/{chat_id}/stop: the cancellation boundary #1474
    #    closed on DELETE, reachable here through a different verb.
    (
        TARGET,
        "        chat = await Chats.get_chat_by_id(chat_id)\n"
        "        if chat is None or (chat.user_id != user.id and user.role != 'admin'):\n"
        "            raise HTTPException(status_code=status.HTTP_404_NOT_FOUND, "
        "detail=ERROR_MESSAGES.NOT_FOUND)\n",
        "        chat = await Chats.get_chat_by_id(chat_id)\n"
        f"        {MARKER}\n"
        "        if chat is None or (\n"
        "            chat.user_id != user.id and not " + FLAGGED + "\n"
        "        ):\n"
        "            raise HTTPException(status_code=status.HTTP_404_NOT_FOUND, "
        "detail=ERROR_MESSAGES.NOT_FOUND)\n",
        1,
    ),
    # 4. The chat-completions ownership check. Beyond issue #1511 as filed, and
    #    the most consequential of the five: it admits a WRITE into another
    #    user's conversation.
    (
        TARGET,
        "                    if not await Chats.is_chat_owner(chat_id, user.id) "
        "and user.role != 'admin':\n",
        f"                    {MARKER}: a write into someone else's chat, so the\n"
        "                    # admin term needs the flag as much as the reads do\n"
        "                    if not await Chats.is_chat_owner(chat_id, user.id) "
        "and not " + FLAGGED + ":\n",
        1,
    ),
]

EXPECTED_MARKERS = {TARGET: 5}


def main():
    already = all(
        (BACKEND / f).read_text().count(MARKER) == n
        for f, n in EXPECTED_MARKERS.items()
    )
    if already:
        print("apply_main_admin_flag_1511_patch: already applied")
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

    final = (BACKEND / TARGET).read_text()

    # No bare admin term may survive on any chat-ownership decision in this file.
    # Counting markers cannot see this: a rewrite that added the flagged form
    # beside the bare one would still count five.
    for stale in (
        "if owner_id != user.id and user.role != 'admin':",
        "if chat is None or (chat.user_id != user.id and user.role != 'admin'):",
        "if not await Chats.is_chat_owner(chat_id, user.id) and user.role != 'admin':",
    ):
        assert stale not in final, (
            f"an unflagged admin bypass survived patching: {stale!r}"
        )

    # And the flag must genuinely be the conjunct, not merely present somewhere
    # in the file. Asked of the AST so that a comment mentioning it cannot pass.
    flagged_sites = 0
    for node in ast.walk(ast.parse(final)):
        if not isinstance(node, ast.BoolOp) or not isinstance(node.op, ast.And):
            continue
        for value in node.values:
            if (
                isinstance(value, ast.BoolOp)
                and isinstance(value.op, ast.And)
                and any(
                    isinstance(v, ast.Name) and v.id == "ENABLE_ADMIN_CHAT_ACCESS"
                    for v in value.values
                )
            ):
                flagged_sites += 1
            elif isinstance(value, ast.UnaryOp) and isinstance(value.op, ast.Not):
                inner = value.operand
                if (
                    isinstance(inner, ast.BoolOp)
                    and isinstance(inner.op, ast.And)
                    and any(
                        isinstance(v, ast.Name) and v.id == "ENABLE_ADMIN_CHAT_ACCESS"
                        for v in inner.values
                    )
                ):
                    flagged_sites += 1
    assert flagged_sites >= EXPECTED_MARKERS[TARGET], (
        f"expected at least {EXPECTED_MARKERS[TARGET]} flag-gated admin terms in "
        f"{TARGET} after patching, found {flagged_sites}"
    )

    for filename, expected in EXPECTED_MARKERS.items():
        count = (BACKEND / filename).read_text().count(MARKER)
        assert count == expected, (
            f"{filename}: {count} markers after patching, expected {expected}"
        )
    print(
        "apply_main_admin_flag_1511_patch: flag-gated 5 bare admin bypasses on "
        "chat ownership in main.py (#1511)"
    )


if __name__ == "__main__":
    main()

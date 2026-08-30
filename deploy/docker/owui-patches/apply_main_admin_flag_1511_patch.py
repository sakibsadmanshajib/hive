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
    # 1. A helper for the channel arm, inserted above the first task endpoint.
    #
    #    WHY THIS EXISTS, since it is the one piece of new logic in the patch.
    #    The socket-pool arm in both endpoints serves TWO prefixes:
    #    `if chat_id.startswith('local:') or chat_id.startswith('channel:'):`,
    #    and the next line is `socket_id = chat_id[len('local:'):]`, a fixed six
    #    character slice. For a `channel:` id, which is eight characters of
    #    prefix, that yields `l:<channel id>`.
    #
    #    Making the slice prefix aware does NOT fix it, and that is worth stating
    #    because it is the obvious reading. The segment after `channel:` is a
    #    CHANNEL id, not a socket id (`main.py`, `channel_id =
    #    chat_id.removeprefix('channel:')`), so `get_user_id_from_session_pool`
    #    returns None for it however the string is sliced. The owner never
    #    resolved, and the bare `user.role != 'admin'` term was the only way any
    #    caller ever got past that comparison.
    #
    #    So flag gating that term without this change would have made a channel
    #    generation uncancellable by ANYONE, the channel's own members included.
    #    That is a functional regression introduced by the fix rather than a
    #    pre-existing gap, which is why it is repaired here and not deferred.
    #
    #    Entitlement for a channel scoped task is channel entitlement, resolved
    #    the way `main.py` already resolves it when such a task is CREATED:
    #    membership for a group or direct message channel, an AccessGrants write
    #    grant otherwise. Deliberately WITHOUT that gate's `user.role != 'admin'`
    #    shortcut, so this patch does not introduce a new unflagged admin bypass
    #    while removing five. The result is that stopping a channel task is
    #    slightly stricter than creating one, which is the safe direction, and
    #    the creation gate's own admin term is recorded as a separate question.
    #
    #    Note also what is NOT done: ENABLE_ADMIN_CHAT_ACCESS is not consulted
    #    here. That flag means cross-tenant CHAT access, and applying it to a
    #    channel scoped id purely because the two shared one branch would be a
    #    conflation rather than a decision.
    (
        TARGET,
        "@app.get('/api/tasks/chat/{chat_id:path}')\n",
        f"{MARKER}: see the patch docstring. A 'channel:' id can never resolve\n"
        "# through the socket pool, so entitlement for a channel scoped task is\n"
        "# channel entitlement, mirroring what main.py requires to CREATE one.\n"
        "async def hive_channel_task_caller_is_entitled(channel_id: str, user) -> bool:\n"
        "    channel = await Channels.get_channel_by_id(channel_id)\n"
        "    if not channel:\n"
        "        return False\n"
        "    if channel.type in ['group', 'dm']:\n"
        "        return await Channels.is_user_channel_member(channel.id, user.id)\n"
        "    return await AccessGrants.has_access(\n"
        "        user_id=user.id,\n"
        "        resource_type='channel',\n"
        "        resource_id=channel.id,\n"
        "        permission='write',\n"
        "    )\n"
        "\n"
        "\n"
        "@app.get('/api/tasks/chat/{chat_id:path}')\n",
        1,
    ),
    # 2. GET /api/tasks/chat/{chat_id}: hands another tenant's task ids to the
    #    caller, which are also the ids POST /api/tasks/stop/{task_id} consumes.
    (
        TARGET,
        "    if chat_id.startswith('local:') or chat_id.startswith('channel:'):\n"
        "        socket_id = chat_id[len('local:') :]\n"
        "        owner_id = get_user_id_from_session_pool(socket_id)\n"
        "        if owner_id != user.id and user.role != 'admin':\n"
        "            return {'task_ids': []}\n"
        "    else:\n"
        "        chat = await Chats.get_chat_by_id(chat_id)\n"
        "        if chat is None or (chat.user_id != user.id and user.role != 'admin'):\n"
        "            return {'task_ids': []}\n",
        f"    {MARKER}: channel scoped, so channel entitlement rather than the\n"
        "    # chat flag, and no socket lookup that could never have resolved\n"
        "    if chat_id.startswith('channel:'):\n"
        "        if not await hive_channel_task_caller_is_entitled(\n"
        "            chat_id.removeprefix('channel:'), user\n"
        "        ):\n"
        "            return {'task_ids': []}\n"
        "    elif chat_id.startswith('local:'):\n"
        "        socket_id = chat_id.removeprefix('local:')\n"
        "        owner_id = get_user_id_from_session_pool(socket_id)\n"
        f"        {MARKER}: bare admin role is not enough; the same flag the\n"
        "        # #1186 family patch uses for chats.py, which compose sets false\n"
        "        if owner_id != user.id and not " + FLAGGED + ":\n"
        "            return {'task_ids': []}\n"
        "    else:\n"
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
        "    if chat_id.startswith('local:') or chat_id.startswith('channel:'):\n"
        "        socket_id = chat_id[len('local:') :]\n"
        "        owner_id = get_user_id_from_session_pool(socket_id)\n"
        "        if owner_id != user.id and user.role != 'admin':\n"
        "            raise HTTPException(status_code=status.HTTP_404_NOT_FOUND, "
        "detail=ERROR_MESSAGES.NOT_FOUND)\n"
        "    else:\n"
        "        chat = await Chats.get_chat_by_id(chat_id)\n"
        "        if chat is None or (chat.user_id != user.id and user.role != 'admin'):\n"
        "            raise HTTPException(status_code=status.HTTP_404_NOT_FOUND, "
        "detail=ERROR_MESSAGES.NOT_FOUND)\n",
        f"    {MARKER}: channel scoped, so channel entitlement rather than the\n"
        "    # chat flag, and no socket lookup that could never have resolved\n"
        "    if chat_id.startswith('channel:'):\n"
        "        if not await hive_channel_task_caller_is_entitled(\n"
        "            chat_id.removeprefix('channel:'), user\n"
        "        ):\n"
        "            raise HTTPException(status_code=status.HTTP_404_NOT_FOUND, "
        "detail=ERROR_MESSAGES.NOT_FOUND)\n"
        "    elif chat_id.startswith('local:'):\n"
        "        socket_id = chat_id.removeprefix('local:')\n"
        "        owner_id = get_user_id_from_session_pool(socket_id)\n"
        f"        {MARKER}: bare admin role is not enough; the same flag the\n"
        "        # #1186 family patch uses for chats.py, which compose sets false\n"
        "        if owner_id != user.id and not " + FLAGGED + ":\n"
        "            raise HTTPException(status_code=status.HTTP_404_NOT_FOUND, "
        "detail=ERROR_MESSAGES.NOT_FOUND)\n"
        "    else:\n"
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

EXPECTED_MARKERS = {TARGET: 8}

# The five chat-ownership decisions this patch rewrites, so exactly five admin
# terms end up conjoined with the flag.
FLAGGED_SITES = 5

# The bare `user.role != 'admin'` checks that stay, pinned BY THEIR TEXT rather
# than by their number. Each was read and deliberately left alone. Reviewed and
# named individually because an earlier draft called them "two feature
# permissions" plus two others, and one of the four is not a feature permission:
#
#   BYPASS_MODEL_ACCESS_CONTROL  model access, with its own flag
#   features.direct_tool_servers a feature permission
#   channel membership           an admin bypass of Channels.is_user_channel_member
#                                on a group or direct-message channel. An
#                                access-control decision, out of scope here only
#                                because ENABLE_ADMIN_CHAT_ACCESS is a statement
#                                about cross-tenant CHAT access and channel
#                                moderation is a distinct administrative
#                                function. Tracked as its own question.
#   ENABLE_PUBLIC_ACTIVE_USERS_COUNT  a public user count, with its own flag
SURVIVING_BARE_CHECKS = (
    "            if not BYPASS_MODEL_ACCESS_CONTROL and (user.role != 'admin' or not BYPASS_ADMIN_ACCESS_CONTROL):\n",
    "            and user.role != 'admin'\n",
    "                if user.role != 'admin':\n",
    "        if not ENABLE_PUBLIC_ACTIVE_USERS_COUNT and user.role != 'admin':\n",
)

# The one POSITIVE-form `user.role == 'admin'` that predates this patch. It
# decides whether a licence entry count appears in the config payload, which is
# not a chat-ownership decision. Pinned by text for the same reason the negated
# survivors are: a new bypass written in the positive form is invisible to a
# count of the negated spelling, and was demonstrated in review.
SURVIVING_POSITIVE_CHECKS = (
    "                    if user.role == 'admin' and user_count is not None\n",
)


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

    # Every `user.role` comparison in this file is now accounted for BY IDENTITY,
    # not by a total. A count says how many of a thing there are; it cannot say
    # whether they are the same things. Two mutations found in review passed a
    # pure count guard: one added a new bypass written in the POSITIVE form
    # (`user.role == 'admin' or ...`), which the negated-spelling count never
    # sees, and one dropped the admin term from an innocuous survivor while
    # adding a real chat-ownership bypass, conserving the total exactly.
    #
    # So both spellings are pinned, and the survivors are pinned individually.
    tree = ast.parse(final)
    negated, positive = [], []
    for node in ast.walk(tree):
        if not isinstance(node, ast.Compare) or len(node.ops) != 1:
            continue
        left, op, right = node.left, node.ops[0], node.comparators[0]
        if not (
            isinstance(left, ast.Attribute)
            and left.attr == "role"
            and isinstance(left.value, ast.Name)
            and left.value.id == "user"
            and isinstance(right, ast.Constant)
            and right.value == "admin"
        ):
            continue
        (negated if isinstance(op, ast.NotEq) else positive).append(node)

    assert len(negated) == len(SURVIVING_BARE_CHECKS), (
        f"expected exactly {len(SURVIVING_BARE_CHECKS)} bare `user.role != "
        f"'admin'` comparisons in {TARGET} after patching, found "
        f"{len(negated)}. A new one is a bypass; a missing one means a survivor "
        "changed shape and this list needs re-deciding, not re-counting"
    )
    for line in SURVIVING_BARE_CHECKS:
        assert final.count(line) == 1, (
            f"the surviving bare admin check {line.strip()!r} is no longer "
            f"present exactly once in {TARGET} (found {final.count(line)}). It "
            "was reviewed and deliberately left alone, so it has either moved, "
            "been rewritten, or been displaced by something else wearing its "
            "count"
        )

    # The positive spelling exists only inside the flag-gated form this patch
    # writes, one per rewritten site. Any other is a bypass written the other
    # way round, which the negated count above is blind to by construction.
    expected_positive = FLAGGED_SITES + len(SURVIVING_POSITIVE_CHECKS)
    assert len(positive) == expected_positive, (
        f"expected exactly {expected_positive} `user.role == 'admin'` "
        f"comparisons in {TARGET} after patching, found {len(positive)}. An "
        "extra one is a bypass written in the positive form"
    )
    for line in SURVIVING_POSITIVE_CHECKS:
        assert final.count(line) == 1, (
            f"the surviving positive admin check {line.strip()!r} is no longer "
            f"present exactly once in {TARGET} (found {final.count(line)})"
        )

    # And each of those must genuinely be conjoined with the flag, rather than
    # merely appearing in a file that mentions it somewhere. Asked of the node.
    flagged = 0
    for node in ast.walk(tree):
        if not isinstance(node, ast.BoolOp) or not isinstance(node.op, ast.And):
            continue
        if not any(
            isinstance(v, ast.Name) and v.id == "ENABLE_ADMIN_CHAT_ACCESS"
            for v in node.values
        ):
            continue
        if any(n in positive for n in node.values):
            flagged += 1
    assert flagged == FLAGGED_SITES, (
        f"expected {FLAGGED_SITES} admin terms conjoined with "
        f"ENABLE_ADMIN_CHAT_ACCESS in {TARGET} after patching, found {flagged}"
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

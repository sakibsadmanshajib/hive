#!/usr/bin/env python3
"""The chat-delete handler must not cancel tasks before it has authorised the caller.

Issue #1474. `DELETE /api/v1/chats/{id}` called `stop_item_tasks` as its first
statement, above the role split and above every ownership check. That function
takes an item id and a Redis handle and cancels every task registered against
that id; it performs no ownership check of its own. So a verified user holding
another user's chat id cancelled that user's in-flight completion, title
generation and tag generation by issuing a DELETE that was then refused with a
404. The refusal was real and the row survived, but the side effect had already
fired. A 404 that has cancelled the victim's streaming response is not a denial.

WHY THIS IS BEHAVIOURAL AND NOT STRUCTURAL
------------------------------------------
scripts/test_owui_chat_delete_authz.py already pins the SHAPE of this handler:
the permission gate, the scoped lookup, the 404 and the delete, in that order.
A statement-ordering assertion for the cancellation would have fitted there
neatly, and would have been worth strictly less than it looked. Asserting that
one call appears below another is a claim about text; the acceptance condition
is a claim about an effect, and the two come apart the moment the handler grows
an early return, a try/finally, or a helper that cancels on the way past.

So this executes the real handler instead. It extracts `delete_chat_by_id` from
the PATCHED router source (the image builds only the frontend from
vendor/open-webui and rewrites routers/chats.py inside the pinned upstream
backend image at build time, so the vendored copy is not the shipped copy),
compiles it, and runs it against recording stubs. What is asserted is the
number of cancellations a call actually performed.

WHY IT RUNS THE PRE-FIX CODE TOO
--------------------------------
A test that only asserts the post-fix behaviour would pass on the buggy code if
the patch silently stopped applying, and it would pass on a fix that removed the
cancellation altogether. Both failures look exactly like success. So the same
driver runs twice, against the same real source:

  * chain WITHOUT apply_chat_delete_task_cancel_1474_patch.py, where a
    non-owner's DELETE must be observed CANCELLING. This is the defect,
    reproduced rather than described. If this leg ever goes quiet, the "after"
    leg below has stopped proving anything and says so.
  * chain WITH it, where a non-owner's DELETE must cancel NOTHING and the
    owner's DELETE must still cancel exactly once.

Both legs cover both arms of the role split. `ENABLE_ADMIN_CHAT_ACCESS` is
"false" on this deployment, so every caller including an administrator takes the
non-admin arm, but the admin arm is driven anyway: a fix that is correct only
because a flag happens to be off is resting on an unrelated setting.

Structural, no framework, no network, no Redis.
Run: python3 scripts/test_owui_chat_delete_task_cancel.py
"""

import ast
import asyncio
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

from test_owui_chat_delete_authz import (  # noqa: E402
    CHATS_PATCHES,
    patched_chats_router,
    route_handler,
)

HANDLER = "delete_chat_by_id"
MARKER = "# hive (#1474)"
EXPECTED_MARKERS = 3
PRE_FIX_PATCHES = tuple(
    p for p in CHATS_PATCHES if p != "apply_chat_delete_task_cancel_1474_patch.py"
)

failures: list[str] = []


def check(condition: bool, message: str) -> None:
    if condition:
        print(f"  ok: {message}")
    else:
        failures.append(message)
        print(f"  FAIL: {message}")


# --- stub collaborators -----------------------------------------------------


class StubHTTPException(Exception):
    def __init__(self, status_code=None, detail=None):
        super().__init__(f"{status_code}: {detail}")
        self.status_code = status_code


class Status:
    HTTP_401_UNAUTHORIZED = 401
    HTTP_404_NOT_FOUND = 404


class ErrorMessages:
    NOT_FOUND = "not found"
    ACCESS_PROHIBITED = "prohibited"

    @staticmethod
    def DEFAULT(*args, **kwargs):
        return "default"


class Events:
    CHAT_DELETED = "chat-deleted"


class StubUser:
    def __init__(self, user_id: str, role: str):
        self.id = user_id
        self.role = role


class StubChat:
    def __init__(self, chat_id: str, user_id: str):
        self.id = chat_id
        self.user_id = user_id
        self.meta = {"tags": []}


class Recorder:
    """Every collaborator the handler reaches, and what it was asked to do."""

    def __init__(self, stored: StubChat | None):
        self.stored = stored
        self.cancelled: list[str] = []
        self.deleted: list[tuple[str, str | None]] = []
        self.permitted = True

    # open_webui.tasks.stop_item_tasks
    async def stop_item_tasks(self, redis, item_id):
        self.cancelled.append(item_id)
        return {"stopped": True}

    # open_webui.utils.access_control.has_permission
    async def has_permission(self, user_id, permission, config):
        return self.permitted


class StubChats:
    def __init__(self, recorder: Recorder):
        self._r = recorder

    async def get_chat_by_id(self, chat_id, db=None):
        stored = self._r.stored
        return stored if stored is not None and stored.id == chat_id else None

    async def get_chat_by_id_and_user_id(self, chat_id, user_id, db=None):
        stored = self._r.stored
        if stored is not None and stored.id == chat_id and stored.user_id == user_id:
            return stored
        return None

    async def delete_orphan_tags_for_user(self, tags, user_id, threshold=None, db=None):
        return None

    async def delete_chat_by_id(self, chat_id, db=None):
        self._r.deleted.append((chat_id, None))
        return True

    async def delete_chat_by_id_and_user_id(self, chat_id, user_id, db=None):
        self._r.deleted.append((chat_id, user_id))
        return True


class StubRequest:
    """Only `request.app.state.redis` is ever read, and only to be handed on."""

    def __init__(self):
        state = type("State", (), {"redis": object()})()
        self.app = type("App", (), {"state": state})()


def compile_handler(source: str, admin_access: bool, recorder: Recorder):
    """The real handler function, lifted out of the patched router and made callable.

    Decorators are dropped (there is no FastAPI router here) and every free name
    the body reaches is bound to a stub. Nothing about the body itself is
    rewritten, so what runs is the source the image runs.
    """
    tree = ast.parse(source)
    func = route_handler(tree)
    if func is None:
        raise SystemExit(f"FAIL: {HANDLER} not found in the patched router")
    func.decorator_list = []

    chats = StubChats(recorder)

    class Config:
        @staticmethod
        async def get(key):
            return {}

    async def publish_event(*args, **kwargs):
        return None

    namespace = {
        # names evaluated when the `def` executes (annotations and defaults)
        "Request": object,
        "AsyncSession": object,
        "Depends": lambda dep: None,
        "get_verified_user": object(),
        "get_async_session": object(),
        # names the body reaches
        "stop_item_tasks": recorder.stop_item_tasks,
        "has_permission": recorder.has_permission,
        "Chats": chats,
        "Config": Config,
        "HTTPException": StubHTTPException,
        "status": Status,
        "ERROR_MESSAGES": ErrorMessages,
        "EVENTS": Events,
        "publish_event": publish_event,
        "ENABLE_ADMIN_CHAT_ACCESS": admin_access,
    }
    module = ast.Module(body=[func], type_ignores=[])
    ast.fix_missing_locations(module)
    exec(compile(module, "<patched chats.py>", "exec"), namespace)  # noqa: S102
    return namespace[HANDLER]


def call(source: str, *, caller: StubUser, chat_id: str, stored: StubChat | None,
         admin_access: bool = False, permitted: bool = True) -> tuple[Recorder, int | None]:
    """Drive one DELETE. Returns the recorder and the refusal status, if any."""
    recorder = Recorder(stored)
    recorder.permitted = permitted
    handler = compile_handler(source, admin_access, recorder)
    refused: int | None = None
    try:
        asyncio.run(handler(StubRequest(), chat_id, user=caller, db=object()))
    except StubHTTPException as exc:
        refused = exc.status_code
    return recorder, refused


# --- the scenarios ----------------------------------------------------------

OWNER = StubUser("owner-1", "user")
STRANGER = StubUser("stranger-2", "user")
ADMIN = StubUser("admin-3", "admin")
CHAT_ID = "e85bb8ac-32f1-4bcb-a5af-2c56060ce571"


def victims_chat() -> StubChat:
    return StubChat(CHAT_ID, OWNER.id)


def run_leg(source: str, *, expect_leak: bool) -> None:
    """Both arms, owner and non-owner, against one patch chain."""

    # 1. Non-admin arm, non-owner. The whole issue.
    recorder, refused = call(source, caller=STRANGER, chat_id=CHAT_ID, stored=victims_chat())
    check(refused == 404, "a non-owner's DELETE is refused with 404")
    check(recorder.deleted == [], "a non-owner's DELETE deletes nothing")
    if expect_leak:
        check(
            recorder.cancelled == [CHAT_ID],
            "PRE-FIX: a non-owner's refused DELETE cancelled the owner's in-flight "
            f"tasks (observed {len(recorder.cancelled)} cancellation(s)); if this "
            "stops reproducing, the post-fix leg below proves nothing",
        )
    else:
        check(
            recorder.cancelled == [],
            "a non-owner's refused DELETE cancels NOTHING (observed "
            f"{len(recorder.cancelled)} cancellation(s))",
        )

    # 2. Non-admin arm, no chat.delete permission. Refused earlier still.
    recorder, refused = call(
        source, caller=STRANGER, chat_id=CHAT_ID, stored=victims_chat(), permitted=False
    )
    check(refused == 401, "a caller without the chat.delete permission is refused with 401")
    if expect_leak:
        check(
            recorder.cancelled == [CHAT_ID],
            "PRE-FIX: a caller refused for lack of the permission still cancelled",
        )
    else:
        check(
            recorder.cancelled == [],
            "a caller refused for lack of the permission cancels nothing",
        )

    # 3. Non-admin arm, the owner. The cancellation must survive the fix.
    recorder, refused = call(source, caller=OWNER, chat_id=CHAT_ID, stored=victims_chat())
    check(refused is None, "the owner's DELETE succeeds")
    check(recorder.deleted == [(CHAT_ID, OWNER.id)], "the owner's DELETE deletes their row")
    check(
        recorder.cancelled == [CHAT_ID],
        "the owner's DELETE still cancels their own in-flight tasks, exactly once",
    )

    # 4. Admin arm, unreachable on this deployment (ENABLE_ADMIN_CHAT_ACCESS is
    #    "false"), driven anyway so the fix does not depend on that flag.
    recorder, refused = call(
        source, caller=ADMIN, chat_id=CHAT_ID, stored=None, admin_access=True
    )
    check(refused == 404, "the admin arm answers 404 for a chat that does not exist")
    if expect_leak:
        check(
            recorder.cancelled == [CHAT_ID],
            "PRE-FIX: the admin arm cancelled before resolving the row",
        )
    else:
        check(
            recorder.cancelled == [],
            "the admin arm cancels nothing when the row does not resolve",
        )

    recorder, refused = call(
        source, caller=ADMIN, chat_id=CHAT_ID, stored=victims_chat(), admin_access=True
    )
    check(refused is None, "the admin arm deletes a chat that does resolve")
    check(
        recorder.cancelled == [CHAT_ID],
        "the admin arm still cancels that chat's in-flight tasks, exactly once",
    )


def main() -> int:
    print("chat delete task cancellation ordering (issue #1474)")

    print("\npre-fix source: patch chain WITHOUT the #1474 patch")
    print(f"  chain: {', '.join(PRE_FIX_PATCHES)}")
    before = patched_chats_router(PRE_FIX_PATCHES)
    check(
        MARKER not in before,
        f"the pre-fix source carries no {MARKER} marker, so this leg really is the "
        "code as it stood",
    )
    run_leg(before, expect_leak=True)

    print("\npost-fix source: the full chain the image runs")
    print(f"  chain: {', '.join(CHATS_PATCHES)}")
    after = patched_chats_router()
    # The patch is verified to have APPLIED, not merely to exist. An
    # exact-literal rewrite whose anchor no longer matches upstream does nothing
    # at all, and that failure is indistinguishable from success from the
    # outside. The patch script asserts its own marker totals and exits non-zero
    # otherwise (patched_chats_router turns that into a SystemExit); this is the
    # independent read-back.
    check(
        after.count(MARKER) == EXPECTED_MARKERS,
        f"the #1474 patch applied: {EXPECTED_MARKERS} {MARKER} markers in the "
        f"patched router (found {after.count(MARKER)})",
    )
    run_leg(after, expect_leak=False)

    print()
    if failures:
        print(f"FAILED: {len(failures)} check(s)")
        for f in failures:
            print(f"  - {f}")
        return 1
    print("PASS")
    return 0


if __name__ == "__main__":
    sys.exit(main())

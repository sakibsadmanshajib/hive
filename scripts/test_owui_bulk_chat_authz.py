#!/usr/bin/env python3
"""Archive All and Delete All must act on the caller's own chats and nothing else.

Issue #866. Settings > Data Controls offers two bulk writes,
`POST /api/v1/chats/archive/all` and `DELETE /api/v1/chats/`. The second one
hard-deletes every conversation the caller owns, along with its messages, and
there is no undo. Both endpoints exist and answer 401 unauthenticated on the
deployed box, so the question this file settles is not whether they are wired
up: it is who they can reach.

WHY A TEST AND NOT A READING
---------------------------
The scoping looks obvious in the vendored source (`filter_by(user_id=user_id)`)
and that appearance is worth very little on its own. The image builds only the
frontend from vendor/open-webui and rewrites routers/chats.py inside the pinned
upstream backend at build time, so the vendored router is not the shipped
router; and the sibling route DELETE /{id} already carries an admin arm with an
unscoped lookup and delete, gated only by ENABLE_ADMIN_CHAT_ACCESS (#1186). On
this deployment every tenant OWNER holds an Open WebUI administrator session
(#748, #948), so an admin arm on a bulk delete would not be a narrow class. This
runs the real patched handlers and records the id they actually hand to the
model layer.

THE PROPERTIES PINNED
---------------------
Handlers (routers/chats.py, on patched source), for both bulk writes:

  1. Neither handler takes any parameter that could name a user or a chat.
     `request`, `user` and `db` are the whole signature, so there is nothing a
     caller could set to aim either write at somebody else. This is the
     structural half of the cross-tenant claim.
  2. Executed, each handler passes the CALLING user's own id to the model, and
     nothing else, for an ordinary user and for an administrator alike.
  3. Neither handler splits on `user.role == 'admin'`, and neither reads
     ENABLE_ADMIN_CHAT_ACCESS. The unscoped arm that exists on DELETE /{id} has
     no counterpart here, so turning that flag on cannot widen these two.
  4. Delete All refuses a caller without the `chat.delete` permission, with 401,
     before it writes anything.
  5. Each handler reaches exactly one model function, the user-scoped one.

Model (models/chats.py), which no owui-patch touches, so the vendored copy is
the shipped copy:

  6. Every statement `delete_chats_by_user_id` and `archive_all_chats_by_user_id`
     execute is keyed on the caller's `user_id`, whether directly through
     `filter_by(user_id=...)` or through a subquery over `Chat.id` that is
     itself keyed on it, and every helper they call is a `*_by_user_id` taking
     that same id. A write that appears keyed on nothing, or on a chat id alone,
     goes red here.

NON-VACUITY
-----------
Property 2's driver is run a second time against a deliberately mutated copy of
the handler, one that hands the model a foreign id. That leg must FAIL the same
assertion the real source passes. Without it, a driver that silently stopped
executing the handler would report the same green as a correctly scoped one.

Structural and behavioural, no framework, no network, no database.
Run: python3 scripts/test_owui_bulk_chat_authz.py
"""

import ast
import asyncio
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

from test_owui_chat_delete_authz import (  # noqa: E402
    find_function,
    is_admin_role_test,
    patched_chats_router,
    vendored_and_pinned_versions,
)

REPO_ROOT = Path(__file__).resolve().parents[1]
VENDORED_MODELS = REPO_ROOT / "vendor/open-webui/backend/open_webui/models/chats.py"

DELETE_HANDLER = "delete_all_user_chats"
ARCHIVE_HANDLER = "archive_all_chats"

DELETE_MODEL = "delete_chats_by_user_id"
ARCHIVE_MODEL = "archive_all_chats_by_user_id"

CALLER_ID = "caller-1"
VICTIM_ID = "victim-9"

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
    ACCESS_PROHIBITED = "prohibited"

    @staticmethod
    def DEFAULT(*args, **kwargs):
        return "default"


class Events:
    CHAT_DELETED_ALL = "chat-deleted-all"
    CHAT_ARCHIVED = "chat-archived"


class StubUser:
    def __init__(self, user_id: str, role: str):
        self.id = user_id
        self.role = role


class Recorder:
    """Every model call the handler made, with the id it was given.

    The id is the whole point. A handler that called the right function with
    somebody else's id would satisfy any name-presence check and still empty a
    stranger's account.
    """

    def __init__(self, permitted: bool = True):
        self.calls: list[tuple[str, str]] = []
        self.permitted = permitted
        self.permission_checks: list[tuple[str, str]] = []

    async def has_permission(self, user_id, permission, config):
        self.permission_checks.append((user_id, permission))
        return self.permitted

    def targets(self, name: str) -> list[str]:
        return [user_id for called, user_id in self.calls if called == name]


class StubChats:
    """Every bulk model function, scoped and unscoped, so reaching for an
    unscoped one is recorded rather than raising a NameError that a scenario
    might mistake for a refusal."""

    def __init__(self, recorder: Recorder):
        self._r = recorder

    def _record(self, name):
        async def call(user_id=None, db=None):
            self._r.calls.append((name, user_id))
            return True

        return call

    def __getattr__(self, name):
        return self._record(name)


class StubRequest:
    def __init__(self):
        state = type("State", (), {"redis": object()})()
        self.app = type("App", (), {"state": state})()


def compile_handler(source: str, name: str, recorder: Recorder, admin_access: bool):
    """The real handler, lifted out of the patched router and made callable.

    Decorators are dropped (there is no FastAPI router here) and every free name
    the body reaches is bound to a stub. Nothing about the body is rewritten, so
    what runs is the source the image runs. Only the one FunctionDef is
    compiled, so the module's imports and top-level statements never execute.
    """
    tree = ast.parse(source)
    func = find_function(tree, name)
    if func is None:
        raise SystemExit(f"FAIL: {name} not found in the patched router")
    func = ast.parse(ast.unparse(func)).body[0]
    func.decorator_list = []

    class Config:
        @staticmethod
        async def get(key):
            return {}

    async def publish_event(*args, **kwargs):
        return None

    namespace = {
        "Request": object,
        "AsyncSession": object,
        "Depends": lambda dep: None,
        "get_verified_user": object(),
        "get_async_session": object(),
        "has_permission": recorder.has_permission,
        "Chats": StubChats(recorder),
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
    return namespace[name]


def call(
    source: str,
    name: str,
    *,
    caller: StubUser,
    admin_access: bool = False,
    permitted: bool = True,
) -> tuple[Recorder, int | None]:
    recorder = Recorder(permitted=permitted)
    handler = compile_handler(source, name, recorder, admin_access)
    refused: int | None = None
    try:
        asyncio.run(handler(StubRequest(), user=caller, db=object()))
    except StubHTTPException as exc:
        refused = exc.status_code
    return recorder, refused


# --- model-level scoping ----------------------------------------------------


def unscoped_statements(source: str, name: str) -> list[str]:
    """Statements in a model function that are not keyed on `user_id`.

    Source text rather than an AST shape walk, because the three writes in
    `delete_chats_by_user_id` are keyed three different ways (a direct
    `filter_by`, an `in_` over a subquery, and a helper call), and a shape
    matcher that recognised only today's three would report a fourth as
    unrecognised or, worse, silently skip it. What every correct one has in
    common is the caller's id, so that is what is looked for.
    """
    tree = ast.parse(source)
    func = find_function(tree, name)
    if func is None:
        raise SystemExit(f"FAIL: {name} not found in {VENDORED_MODELS}")

    offenders: list[str] = []
    for node in ast.walk(func):
        if not isinstance(node, ast.Call):
            continue
        callee = node.func
        if isinstance(callee, ast.Attribute) and callee.attr == "execute":
            segment = ast.get_source_segment(source, node) or ""
            if "user_id" not in segment:
                offenders.append(" ".join(segment.split())[:120])
        # Helper calls on self, which is how delete_shared_chats_by_user_id is
        # reached. A helper that is not user scoped by name and by argument is
        # a hole whatever its body does.
        if (
            isinstance(callee, ast.Attribute)
            and isinstance(callee.value, ast.Name)
            and callee.value.id == "self"
        ):
            segment = ast.get_source_segment(source, node) or ""
            if not callee.attr.endswith("_by_user_id") or "user_id" not in segment:
                offenders.append(" ".join(segment.split())[:120])
    return offenders


SELFCHECK_MODEL = '''
class Chats:
    async def scoped(self, user_id, db=None):
        async with ctx(db) as session:
            await session.execute(delete(Chat).filter_by(user_id=user_id))
            await self.delete_shared_chats_by_user_id(user_id, db=session)

    async def leaky(self, user_id, db=None):
        async with ctx(db) as session:
            await session.execute(delete(Chat))
            await self.delete_shared_chat_by_chat_id(chat_id, db=session)
'''


def selfcheck_unscoped_detector() -> None:
    """The detector above must find the hole in a function that has one.

    A checker that reported "no unscoped statements" because it never matched
    anything would pass this file's real assertions unchanged.
    """
    print("selfcheck: unscoped-statement detector")
    check(unscoped_statements(SELFCHECK_MODEL, "scoped") == [], "a scoped function reads clean")
    leaky = unscoped_statements(SELFCHECK_MODEL, "leaky")
    check(len(leaky) == 2, f"a leaky function reports both of its holes (got {len(leaky)})")


# --- scenarios --------------------------------------------------------------

ORDINARY = StubUser(CALLER_ID, "user")
ADMIN = StubUser(CALLER_ID, "admin")
ADMIN_OTHER = StubUser("admin-3", "admin")


def run_scoping_leg(source: str, *, expect_scoped: bool, label: str) -> None:
    """Both handlers, both roles, both settings of the admin flag."""
    for handler, model in ((DELETE_HANDLER, DELETE_MODEL), (ARCHIVE_HANDLER, ARCHIVE_MODEL)):
        for caller in (ORDINARY, ADMIN):
            for admin_access in (False, True):
                recorder, refused = call(
                    source, handler, caller=caller, admin_access=admin_access
                )
                targets = recorder.targets(model)
                scoped = targets == [caller.id]
                described = (
                    f"{label}: {handler} as {caller.role} "
                    f"(ENABLE_ADMIN_CHAT_ACCESS={admin_access}) "
                    f"writes only the caller's own chats"
                )
                if expect_scoped:
                    check(scoped, f"{described} (refused={refused}, targets={targets})")
                else:
                    check(
                        not scoped,
                        f"MUTANT: {described} must NOT hold on the mutated source "
                        f"(targets={targets}); if it does, this driver is not "
                        f"executing the handler and the real leg proves nothing",
                    )


def main() -> int:
    vendored, pinned = vendored_and_pinned_versions()
    print(f"vendored frontend v{vendored}, pinned backend image v{pinned}")
    check(
        vendored is not None and vendored == pinned,
        "the vendored tree and the pinned backend image are the same version, so "
        "patching the vendored router describes the source the image runs",
    )

    source = patched_chats_router()
    tree = ast.parse(source)

    print("\nsignatures: nothing a caller could aim at somebody else")
    for handler in (DELETE_HANDLER, ARCHIVE_HANDLER):
        func = find_function(tree, handler)
        check(func is not None, f"{handler} is present in the patched router")
        if func is None:
            continue
        args = [a.arg for a in func.args.args]
        check(
            args == ["request", "user", "db"],
            f"{handler} takes only request, user and db (got {args})",
        )
        check(
            func.args.kwonlyargs == [] and func.args.vararg is None and func.args.kwarg is None,
            f"{handler} takes no further arguments",
        )

    print("\nno admin arm, so ENABLE_ADMIN_CHAT_ACCESS cannot widen either write")
    for handler in (DELETE_HANDLER, ARCHIVE_HANDLER):
        func = find_function(tree, handler)
        admin_tests = [
            node for node in ast.walk(func) if isinstance(node, ast.If) and is_admin_role_test(node.test)
        ]
        check(admin_tests == [], f"{handler} does not split on user.role == 'admin'")
        names = {node.id for node in ast.walk(func) if isinstance(node, ast.Name)}
        check(
            "ENABLE_ADMIN_CHAT_ACCESS" not in names,
            f"{handler} never reads ENABLE_ADMIN_CHAT_ACCESS",
        )

    print("\nexecuted: the id each handler hands the model layer")
    run_scoping_leg(source, expect_scoped=True, label="shipped")

    print("\nexecuted: one model function each, and it is the scoped one")
    recorder, _ = call(source, DELETE_HANDLER, caller=ORDINARY)
    check(
        [name for name, _ in recorder.calls] == [DELETE_MODEL],
        f"delete-all reaches {DELETE_MODEL} and nothing else "
        f"(got {[name for name, _ in recorder.calls]})",
    )
    recorder, _ = call(source, ARCHIVE_HANDLER, caller=ORDINARY)
    check(
        [name for name, _ in recorder.calls] == [ARCHIVE_MODEL],
        f"archive-all reaches {ARCHIVE_MODEL} and nothing else "
        f"(got {[name for name, _ in recorder.calls]})",
    )

    print("\nexecuted: the chat.delete permission gate on delete-all")
    recorder, refused = call(source, DELETE_HANDLER, caller=ORDINARY, permitted=False)
    check(refused == 401, f"an ordinary caller without chat.delete is refused (got {refused})")
    check(recorder.calls == [], "and nothing was deleted before the refusal")
    check(
        recorder.permission_checks == [(CALLER_ID, "chat.delete")],
        f"the gate asked about the caller's own chat.delete permission "
        f"(got {recorder.permission_checks})",
    )

    print("\nnon-vacuity: the same driver against a handler aimed elsewhere")
    mutated = source.replace(
        f"Chats.{DELETE_MODEL}(user.id", f"Chats.{DELETE_MODEL}('{VICTIM_ID}'"
    ).replace(f"Chats.{ARCHIVE_MODEL}(user.id", f"Chats.{ARCHIVE_MODEL}('{VICTIM_ID}'")
    check(
        mutated != source,
        "the mutation applied (if the call site is rewritten, this anchor must move with it)",
    )
    run_scoping_leg(mutated, expect_scoped=False, label="mutant")

    print("\nmodel: every write keyed on the caller's id")
    selfcheck_unscoped_detector()
    models = VENDORED_MODELS.read_text(encoding="utf-8")
    for model in (DELETE_MODEL, ARCHIVE_MODEL):
        offenders = unscoped_statements(models, model)
        check(
            offenders == [],
            f"{model} keys every statement on user_id (unkeyed: {offenders})",
        )

    print()
    if failures:
        print(f"FAILED ({len(failures)}):")
        for failure in failures:
            print(f"  - {failure}")
        return 1
    print("PASS")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

#!/usr/bin/env python3
"""main.py's chat task endpoints must honour ENABLE_ADMIN_CHAT_ACCESS.

Issue #1511, third sibling from the security review of PR #1496. Unlike #1474
and #1508 this is not a pre-authorisation side effect: both endpoints resolve
the chat before acting. What was wrong is the WIDTH of the predicate, a bare
`user.role != 'admin'` with no flag term, which is exactly the shape
`apply_router_authz_family_patch.py` closes for issue #1186 on every router.
main.py was never in that patch's file set.

WHO CAN REACH IT, WHICH IS NOT WHAT THE ISSUE SAID
--------------------------------------------------
The issue as filed claimed every tenant OWNER holds an administrator session.
That is FALSE on this deployment and the correction is the point of this
docstring. `owui-patches/tenant_role_from_db.py` resolves the Open WebUI role
from Hive's own Postgres and grants 'admin' only to a login owning an account
with `is_platform_admin = true`; an ordinary tenant OWNER resolves to 'user'.
So the reachable class is Hive platform staff, not customers.

It is still worth closing, because setting ENABLE_ADMIN_CHAT_ACCESS to "false"
is this deployment's statement that not even a platform admin gets cross-tenant
chat access through the product surface, and these endpoints ignored it.

WHAT IS ASSERTED
----------------
Behaviourally, to the standard #1474 set: the pre-fix code is OBSERVED handing a
non-owner administrator another user's task ids and cancelling their tasks, and
the post-fix code observed refusing. The flag is then turned ON and the same
administrator is observed allowed again, which is what distinguishes "gated on
the flag" from "admins locked out".

The fifth site the patch rewrites, the chat-completions ownership check, is NOT
driven here: it sits deep inside a several-hundred-line handler that cannot be
lifted out and executed against stubs the way these two can. It is covered
structurally instead, by the patch's own AST postcondition, and this file says
so rather than implying the behavioural coverage extends to it.

Structural, no framework, no network, no Redis.
Run: python3 scripts/test_owui_main_admin_flag.py
"""

import ast
import asyncio
import os
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

from test_owui_chat_delete_authz import vendored_and_pinned_versions  # noqa: E402

REPO_ROOT = Path(__file__).resolve().parents[1]
VENDORED_BACKEND = REPO_ROOT / "vendor/open-webui/backend/open_webui"
PATCHES = REPO_ROOT / "deploy/docker/owui-patches"
COMPOSE = REPO_ROOT / "deploy/docker/docker-compose.yml"

PATCH = "apply_main_admin_flag_1511_patch.py"
MARKER = "# hive (#1511)"
EXPECTED_MARKERS = 8

LIST_ENDPOINT = "list_tasks_by_chat_id_endpoint"
STOP_ENDPOINT = "stop_tasks_by_chat_id_endpoint"
# Present only in the patched source. The channel arm calls it, so it has to be
# compiled alongside the endpoints; absent in the pre-fix leg, where the arm
# that would call it does not exist either.
CHANNEL_HELPER = "hive_channel_task_caller_is_entitled"

VICTIM_CHAT_ID = "e85bb8ac-32f1-4bcb-a5af-2c56060ce571"
VICTIM_ID = "owner-1"

# The socket-scoped arm. `local:<socket id>` names a socket the server holds;
# `channel:<channel id>` names a channel and can never resolve through the
# session pool, whatever the slice, because a channel id is not a socket id.
VICTIM_SOCKET = "sid-victim-socket"
VICTIM_LOCAL_ID = f"local:{VICTIM_SOCKET}"
CHANNEL_ID = "5f0b7e3a-1111-2222-3333-444455556666"
VICTIM_CHANNEL_ID = f"channel:{CHANNEL_ID}"

# Only this socket exists. Anything else resolves to None, which is what the
# real pool does for an unknown or disconnected sid.
SESSION_POOL = {VICTIM_SOCKET: VICTIM_ID}

failures: list[str] = []


def check(condition: bool, message: str) -> None:
    if condition:
        print(f"  ok: {message}")
    else:
        failures.append(message)
        print(f"  FAIL: {message}")


def patched_main(apply_1511: bool) -> str:
    """main.py as the image runs it, with or without the #1511 patch."""
    with tempfile.TemporaryDirectory(prefix="owui-main-admin-") as tmpdir:
        tmp = Path(tmpdir)
        shutil.copy(VENDORED_BACKEND / "main.py", tmp / "main.py")
        if apply_1511:
            env = dict(os.environ)
            env["HIVE_OWUI_BACKEND_DIR"] = str(tmp)
            result = subprocess.run(
                [sys.executable, str(PATCHES / PATCH)],
                env=env,
                capture_output=True,
                text=True,
            )
            if result.returncode != 0:
                raise SystemExit(
                    f"FAIL: {PATCH} failed:\n{result.stdout}{result.stderr}"
                )
        return (tmp / "main.py").read_text(encoding="utf-8")


class StubHTTPException(Exception):
    def __init__(self, status_code=None, detail=None):
        super().__init__(f"{status_code}: {detail}")
        self.status_code = status_code


class Status:
    HTTP_401_UNAUTHORIZED = 401
    HTTP_404_NOT_FOUND = 404


class ErrorMessages:
    NOT_FOUND = "not found"

    @staticmethod
    def DEFAULT(*a, **k):
        return "default"


class User:
    def __init__(self, user_id: str, role: str):
        self.id = user_id
        self.role = role


class Chat:
    def __init__(self, chat_id: str, user_id: str):
        self.id = chat_id
        self.user_id = user_id


class Recorder:
    def __init__(self):
        self.cancelled: list[str] = []

    async def stop_item_tasks(self, redis, item_id):
        self.cancelled.append(item_id)
        return {"stopped": True}


def compile_endpoints(source: str, admin_access: bool, recorder: Recorder):
    """Both task endpoints, lifted out of the patched module and made callable.

    Only the two `AsyncFunctionDef` nodes are compiled, so main.py's module-level
    imports and its FastAPI app construction never run. Executing vendored code
    is deliberate and is the same trust assumption the image build already makes
    on this tree.
    """
    tree = ast.parse(source)
    wanted = {}
    for node in ast.walk(tree):
        if (
            isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef))
            and node.name in (LIST_ENDPOINT, STOP_ENDPOINT, CHANNEL_HELPER)
        ):
            node.decorator_list = []
            wanted[node.name] = node
    missing = {LIST_ENDPOINT, STOP_ENDPOINT} - set(wanted)
    if missing:
        raise SystemExit(f"FAIL: endpoints not found in patched source: {missing}")

    class Chats:
        @staticmethod
        async def get_chat_by_id(chat_id):
            return Chat(chat_id, VICTIM_ID) if chat_id == VICTIM_CHAT_ID else None

    class Log:
        @staticmethod
        def debug(*a, **k):
            return None

        info = warning = error = debug

    async def list_task_ids_by_item_id(redis, item_id):
        return [f"task-for-{item_id}"]

    class Channel:
        def __init__(self, channel_id: str):
            self.id = channel_id
            self.type = "group"

    class Channels:
        @staticmethod
        async def get_channel_by_id(channel_id):
            return Channel(channel_id) if channel_id == CHANNEL_ID else None

        @staticmethod
        async def is_user_channel_member(channel_id, user_id):
            return user_id == VICTIM_ID

    class AccessGrants:
        @staticmethod
        async def has_access(**kwargs):
            return False

    namespace = {
        "Request": object,
        "Depends": lambda dep: None,
        "get_verified_user": object(),
        "Chats": Chats,
        # A real session pool answers None for a socket it does not hold
        # (socket/main.py: SESSION_POOL.get(sid)), and entries are removed on
        # disconnect. A stub that answers the victim id for EVERY socket makes
        # the None branch, the wrong-socket branch and the reconnected-socket
        # branch invisible, and those are exactly the paths a bad slice breaks.
        "get_user_id_from_session_pool": SESSION_POOL.get,
        "Channels": Channels,
        "AccessGrants": AccessGrants,
        "list_task_ids_by_item_id": list_task_ids_by_item_id,
        "stop_item_tasks": recorder.stop_item_tasks,
        "HTTPException": StubHTTPException,
        "status": Status,
        "ERROR_MESSAGES": ErrorMessages,
        "log": Log,
        "ENABLE_ADMIN_CHAT_ACCESS": admin_access,
    }
    body = [wanted[LIST_ENDPOINT], wanted[STOP_ENDPOINT]]
    if CHANNEL_HELPER in wanted:
        body.insert(0, wanted[CHANNEL_HELPER])
    module = ast.Module(body=body, type_ignores=[])
    ast.fix_missing_locations(module)
    exec(compile(module, "<patched main.py>", "exec"), namespace)  # noqa: S102
    return namespace[LIST_ENDPOINT], namespace[STOP_ENDPOINT]


class Req:
    def __init__(self):
        state = type("State", (), {"redis": object()})()
        self.app = type("App", (), {"state": state})()


def call_list(source, *, user, chat_id, admin_access=False):
    recorder = Recorder()
    list_ep, _ = compile_endpoints(source, admin_access, recorder)
    return asyncio.run(list_ep(Req(), chat_id, user=user)), recorder


def call_stop(source, *, user, chat_id, admin_access=False):
    recorder = Recorder()
    _, stop_ep = compile_endpoints(source, admin_access, recorder)
    refused = None
    try:
        asyncio.run(stop_ep(Req(), chat_id, user=user))
    except StubHTTPException as exc:
        refused = exc.status_code
    return refused, recorder


OWNER = User(VICTIM_ID, "user")
STRANGER = User("stranger-2", "user")
ADMIN = User("platform-staff-3", "admin")


def run_leg(source: str, *, expect_leak: bool) -> None:
    # 1. A non-owner ADMIN, with the flag OFF, which is the compose default.
    listed, _ = call_list(source, user=ADMIN, chat_id=VICTIM_CHAT_ID, admin_access=False)
    refused, rec = call_stop(source, user=ADMIN, chat_id=VICTIM_CHAT_ID, admin_access=False)
    if expect_leak:
        check(
            listed["task_ids"] != [],
            "PRE-FIX: a non-owner administrator was handed the victim's task ids "
            f"with ENABLE_ADMIN_CHAT_ACCESS off (observed {listed['task_ids']})",
        )
        check(
            refused is None and rec.cancelled == [VICTIM_CHAT_ID],
            "PRE-FIX: and cancelled the victim's tasks "
            f"(refused={refused}, cancelled={rec.cancelled})",
        )
    else:
        check(
            listed["task_ids"] == [],
            "a non-owner administrator is handed NO task ids while the flag is off "
            f"(observed {listed['task_ids']})",
        )
        check(
            refused == 404 and rec.cancelled == [],
            "and cancels nothing, refused with 404 "
            f"(refused={refused}, cancelled={rec.cancelled})",
        )

    # 2. The same administrator with the flag ON. This is what separates
    #    "gated on the flag" from "administrators locked out", and it must be
    #    true in BOTH legs.
    listed, _ = call_list(source, user=ADMIN, chat_id=VICTIM_CHAT_ID, admin_access=True)
    refused, rec = call_stop(source, user=ADMIN, chat_id=VICTIM_CHAT_ID, admin_access=True)
    check(
        listed["task_ids"] != [] and refused is None and rec.cancelled == [VICTIM_CHAT_ID],
        "with ENABLE_ADMIN_CHAT_ACCESS ON the administrator is allowed again, so "
        "the flag is what decides "
        f"(listed={listed['task_ids']}, refused={refused}, cancelled={rec.cancelled})",
    )

    # 3. The owner is unaffected in every configuration.
    listed, _ = call_list(source, user=OWNER, chat_id=VICTIM_CHAT_ID, admin_access=False)
    refused, rec = call_stop(source, user=OWNER, chat_id=VICTIM_CHAT_ID, admin_access=False)
    check(
        listed["task_ids"] != [] and refused is None and rec.cancelled == [VICTIM_CHAT_ID],
        "the owner still lists and stops their own tasks "
        f"(listed={listed['task_ids']}, refused={refused})",
    )

    # 4. An ordinary non-owner was always refused and still is.
    listed, _ = call_list(source, user=STRANGER, chat_id=VICTIM_CHAT_ID, admin_access=False)
    refused, rec = call_stop(source, user=STRANGER, chat_id=VICTIM_CHAT_ID, admin_access=False)
    check(
        listed["task_ids"] == [] and refused == 404 and rec.cancelled == [],
        "an ordinary non-owner is refused on both endpoints "
        f"(listed={listed['task_ids']}, refused={refused})",
    )



def run_socket_arm(source: str, *, post_fix: bool) -> None:
    """The `local:` and `channel:` arm, which the chat scenarios never reach.

    Split out rather than folded into run_leg because the channel half has no
    pre-fix and post-fix symmetry to assert: before the fix nobody but an
    administrator could ever pass it, and that is the regression being repaired.
    """
    # 5. The owner of a live temporary chat, named by their own socket.
    listed, _ = call_list(source, user=OWNER, chat_id=VICTIM_LOCAL_ID)
    refused, _ = call_stop(source, user=OWNER, chat_id=VICTIM_LOCAL_ID)
    check(
        listed["task_ids"] != [] and refused is None,
        "the owner of a temporary chat still lists and stops it by their own "
        f"socket id (listed={listed['task_ids']}, refused={refused})",
    )

    # 6. A stranger naming somebody else's socket. The pool resolves the real
    #    owner, and the comparison is against the caller's authenticated id.
    listed, _ = call_list(source, user=STRANGER, chat_id=VICTIM_LOCAL_ID)
    refused, _ = call_stop(source, user=STRANGER, chat_id=VICTIM_LOCAL_ID)
    check(
        listed["task_ids"] == [] and refused == 404,
        "a stranger naming someone else's socket is refused "
        f"(listed={listed['task_ids']}, refused={refused})",
    )

    # 7. A socket the pool does not hold, which is what a disconnected or
    #    invented sid looks like. It must FAIL CLOSED, and the owner is used as
    #    the caller precisely because an arm that fell open would pass with a
    #    stranger.
    unknown = "local:sid-not-in-the-pool"
    listed, _ = call_list(source, user=OWNER, chat_id=unknown)
    refused, _ = call_stop(source, user=OWNER, chat_id=unknown)
    check(
        listed["task_ids"] == [] and refused == 404,
        "an unresolvable socket id denies rather than admits, even for the "
        f"owner (listed={listed['task_ids']}, refused={refused})",
    )

    # 8. The channel arm. THIS IS THE REGRESSION CHECK. `chat_id[len('local:'):]`
    #    is a fixed six-character slice, so a `channel:` id yielded
    #    `l:<channel id>` and the owner never resolved; the bare admin term was
    #    the only way anything passed. Flag-gating that term without repairing
    #    the arm makes a channel generation uncancellable by anyone.
    listed, _ = call_list(source, user=OWNER, chat_id=VICTIM_CHANNEL_ID)
    refused, _ = call_stop(source, user=OWNER, chat_id=VICTIM_CHANNEL_ID)
    if post_fix:
        check(
            listed["task_ids"] != [] and refused is None,
            "a channel member lists and stops their channel's generation "
            f"(listed={listed['task_ids']}, refused={refused})",
        )
    else:
        check(
            listed["task_ids"] == [] and refused == 404,
            "PRE-FIX: a channel member could NOT stop their own channel's "
            "generation, because the socket slice never resolved one "
            f"(listed={listed['task_ids']}, refused={refused})",
        )

    # 9. A non-member on the same channel is refused in both legs.
    listed, _ = call_list(source, user=STRANGER, chat_id=VICTIM_CHANNEL_ID)
    refused, _ = call_stop(source, user=STRANGER, chat_id=VICTIM_CHANNEL_ID)
    check(
        listed["task_ids"] == [] and refused == 404,
        "a non-member is refused on the channel arm "
        f"(listed={listed['task_ids']}, refused={refused})",
    )


def check_site_five(after: str) -> None:
    """The chat-completions ownership check, pinned by POSITION as well as text.

    Asserting the predicate's text alone answers a narrower question than it
    looks: a refactor that kept the line verbatim and moved it somewhere it
    never executes passes. So the node is located in the AST and its ancestry is
    asserted, which is the same identity-over-text rule the patch postconditions
    now follow.
    """
    tree = ast.parse(after)
    handler = next(
        (
            n
            for n in ast.walk(tree)
            if isinstance(n, (ast.FunctionDef, ast.AsyncFunctionDef))
            and n.name == "chat_completion"
        ),
        None,
    )
    check(handler is not None, "the chat-completions handler is still called chat_completion")
    if handler is None:
        return

    # The guarded `raise` must still be the body of an `if` whose test is the
    # flag-gated ownership predicate, and that `if` must sit on the `else:`
    # branch of the is-new-chat split, which is where an EXISTING chat is
    # handled. On the new-chat branch there is no owner to check yet.
    sites = []
    for node in ast.walk(handler):
        if not isinstance(node, ast.If):
            continue
        for sub in node.orelse:
            for inner in ast.walk(sub):
                if not isinstance(inner, ast.If):
                    continue
                text = ast.unparse(inner.test)
                if "is_chat_owner" not in text:
                    continue
                sites.append((inner, text))

    check(
        len(sites) == 1,
        f"exactly one is_chat_owner gate on an else branch of the "
        f"chat-completions handler (found {len(sites)})",
    )
    if len(sites) != 1:
        return
    node, text = sites[0]
    check(
        "ENABLE_ADMIN_CHAT_ACCESS" in text,
        f"and its predicate is flag-gated (unparsed: {text})",
    )
    check(
        "user.role != 'admin'" not in text,
        "and carries no bare admin term (unparsed above)",
    )
    check(
        any(isinstance(s, ast.Raise) for s in ast.walk(ast.Module(body=list(node.body), type_ignores=[]))),
        "and the guarded body still raises rather than having been emptied",
    )


def main() -> int:
    print("main.py chat task endpoints honour ENABLE_ADMIN_CHAT_ACCESS (issue #1511)")

    vendored_version, pinned_version = vendored_and_pinned_versions()
    check(
        vendored_version is not None and vendored_version == pinned_version,
        "the vendored tree and the pinned backend image are the same open-webui "
        f"version (vendor={vendored_version}, pinned={pinned_version})",
    )

    compose = COMPOSE.read_text(encoding="utf-8")
    check(
        'ENABLE_ADMIN_CHAT_ACCESS: "false"' in compose,
        "docker-compose.yml still sets ENABLE_ADMIN_CHAT_ACCESS false, so the "
        "flag-off leg below is the deployed configuration and not a hypothetical",
    )

    print("\npre-fix source: main.py WITHOUT the #1511 patch")
    before = patched_main(apply_1511=False)
    check(MARKER not in before, f"the pre-fix source carries no {MARKER} marker")
    run_leg(before, expect_leak=True)
    run_socket_arm(before, post_fix=False)

    print("\npost-fix source: with the #1511 patch")
    after = patched_main(apply_1511=True)
    check(
        after.count(MARKER) == EXPECTED_MARKERS,
        f"the #1511 patch applied: {EXPECTED_MARKERS} {MARKER} markers "
        f"(found {after.count(MARKER)})",
    )
    run_leg(after, expect_leak=False)
    run_socket_arm(after, post_fix=True)

    print("\nthe chat-completions ownership check")
    check(
        "if not await Chats.is_chat_owner(chat_id, user.id) and user.role != 'admin':"
        not in after,
        "the completions-path ownership check no longer carries a bare admin term",
    )
    check_site_five(after)

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

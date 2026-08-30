#!/usr/bin/env python3
"""ydoc:document:update must not touch the task keyspace with an unvalidated id.

Issue #1508, sibling of #1474. `open_webui.tasks` keys every task by an item id,
chat completions register under the bare chat id, and `stop_item_tasks` cancels
whatever is registered under the id it is handed. The Yjs collaboration handler
used that same registry as a debounce, keyed on a `document_id` taken straight
off the socket frame, and resolved ownership only for `note:`-prefixed ids. So a
verified user emitting an update for a bare chat UUID cancelled that chat's
in-flight completion, title generation and tag generation.

WHAT IS ASSERTED, AND HOW
-------------------------
Behaviourally, to the standard #1474 set: the pre-fix code must be OBSERVED
cancelling and the post-fix code observed not cancelling. A structural assertion
that one call sits inside an `if` would be a claim about text; the acceptance
condition is a claim about an effect.

So this extracts the real `yjs_document_update` from the PATCHED source (the
image builds only the frontend from vendor/open-webui and rewrites the Python
backend inside the pinned upstream image at build time, so the vendored copy is
not the shipped copy), compiles it, and runs it against recording stubs. What is
asserted is which item ids the handler actually handed to the task registry.

The same driver runs against the patch chain WITHOUT the #1508 patch and WITH
it, because a test that only asserted the post-fix behaviour would pass on the
buggy code if the patch silently stopped applying, and would also pass on a
"fix" that removed the debounce altogether and broke note collaboration.

THE ROOM-MEMBERSHIP TEST IS NOT THE BOUNDARY, AND IS NOT TREATED AS ONE
----------------------------------------------------------------------
The handler requires `sid` to be in `doc_<document_id>` before it acts. Every
scenario below therefore puts the caller in the room, because a real attacker
puts themselves there: `ydoc:document:join` applies the same `note:`-only check
and then enters the room unconditionally for any other id. Modelling the
membership test as an obstacle would make this suite prove something no attacker
would encounter.

That is also why the fix does not tighten it. Entitlement is a property of the
caller's identity, never of their connection, and a guard that keys on the
socket the caller already controls is the defect wearing a fix's clothing.

Structural, no framework, no network, no Redis, no socket.
Run: python3 scripts/test_owui_ydoc_task_cancel.py
"""

import ast
import asyncio
import os
import shutil
import subprocess
import sys
import tempfile
from collections.abc import Awaitable, Callable
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

from test_owui_chat_delete_authz import vendored_and_pinned_versions  # noqa: E402

REPO_ROOT = Path(__file__).resolve().parents[1]
VENDORED_BACKEND = REPO_ROOT / "vendor/open-webui/backend/open_webui"
PATCHES = REPO_ROOT / "deploy/docker/owui-patches"

HANDLER = "yjs_document_update"
MARKER = "# hive (#1508)"
EXPECTED_MARKERS = 2
PATCH = "apply_ydoc_task_cancel_1508_patch.py"

# A chat id of the shape the capture recorded, and a note id. The whole defect is
# that the handler could not tell these apart.
VICTIM_CHAT_ID = "e85bb8ac-32f1-4bcb-a5af-2c56060ce571"
NOTE_ID = "3788a416-e696-434d-baa5-c152a2b2ea87"
OWNER_SID = "sid-owner"
STRANGER_SID = "sid-stranger"

# Two more shapes the chat task keyspace actually holds. `main.py` registers a
# completion with `create_task(..., id=chat_id)` (line 1638), and chat_id is
# `local:<socket_id>` for a temporary chat and `channel:<channel_id>` for a
# channel message; both are branched on in main.py and utils/middleware.py. A
# suite that attacks only with bare UUIDs cannot tell the shipped
# `startswith('note:')` guard from one keyed on a colon, which would leave every
# temporary chat and every channel completion cancellable.
VICTIM_LOCAL_ID = "local:sid-victim-socket"
VICTIM_CHANNEL_ID = "channel:5f0b7e3a-1111-2222-3333-444455556666"


def SESSION_POOL_FOR(recorder: "Recorder") -> dict[str, dict[str, str]]:
    """Every sid a scenario may use, and no others. See emit()."""
    return {
        OWNER_SID: {"id": recorder.note_owner_id, "role": "user"},
        STRANGER_SID: {"id": "stranger-2", "role": "user"},
    }

failures: list[str] = []


def check(condition: bool, message: str) -> None:
    if condition:
        print(f"  ok: {message}")
    else:
        failures.append(message)
        print(f"  FAIL: {message}")


# --- shipped source ---------------------------------------------------------


def patched_socket_main(apply_1508: bool) -> str:
    """socket/main.py as the image runs it, with or without the #1508 patch.

    No other owui-patch touches this file, so the chain is this patch alone. A
    patch failure is fatal rather than falling back to the vendored text, which
    would turn the post-fix leg into a second copy of the pre-fix leg.
    """
    with tempfile.TemporaryDirectory(prefix="owui-ydoc-authz-") as tmpdir:
        tmp = Path(tmpdir)
        (tmp / "socket").mkdir()
        shutil.copy(VENDORED_BACKEND / "socket/main.py", tmp / "socket/main.py")
        if apply_1508:
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
        return (tmp / "socket/main.py").read_text(encoding="utf-8")


# --- stub collaborators -----------------------------------------------------


class Note:
    def __init__(self, note_id: str, user_id: str):
        self.id = note_id
        self.user_id = user_id


class Recorder:
    """Which item ids the handler handed to the shared task registry."""

    def __init__(self, note_owner_id: str):
        self.note_owner_id = note_owner_id
        self.cancelled: list[str] = []
        self.registered: list[str] = []
        self.saved: list[str] = []
        # The real handler wraps its whole body in `except Exception as e:
        # log.error(...)`. With log.error a no-op, ANY early exception (a stub
        # shape mismatch, an upstream refactor, a missing payload key) produces
        # exactly the observable state a working guard produces: cancelled == []
        # and registered == []. Recording the error calls is what separates
        # "the guard blocked it" from "the handler crashed before reaching it".
        self.errors: list[str] = []

    async def stop_item_tasks(self, redis, item_id):
        self.cancelled.append(item_id)
        return {"stopped": True}

    async def create_task(self, redis, coroutine, item_id=None):
        # The real create_task schedules the coroutine; closing it here keeps
        # the recorder honest about WHAT was registered without running a
        # debounce timer inside a unit test.
        coroutine.close()
        self.registered.append(item_id)
        return ("task-id", None)


def compile_handler(
    source: str, recorder: Recorder, membership: list[str]
) -> Callable[..., Awaitable[None]]:
    """The real handler, lifted out of the patched module and made callable.

    Only the one `AsyncFunctionDef` is compiled, so socket/main.py's module-level
    imports and its `sio` construction never run, and the body executes only when
    a scenario calls it. Executing vendored code here is deliberate and is the
    same trust assumption the image build already makes on this tree.
    """
    tree = ast.parse(source)
    func = next(
        (
            n
            for n in ast.walk(tree)
            if isinstance(n, (ast.FunctionDef, ast.AsyncFunctionDef))
            and n.name == HANDLER
        ),
        None,
    )
    if func is None:
        raise SystemExit(f"FAIL: {HANDLER} not found in the patched source")
    func.decorator_list = []

    class Notes:
        @staticmethod
        async def get_note_by_id(note_id):
            return Note(note_id, recorder.note_owner_id) if note_id == NOTE_ID else None

    class AccessGrants:
        @staticmethod
        async def has_access(**kwargs):
            return False

    class Sio:
        @staticmethod
        async def emit(*args, **kwargs):
            return None

    class YdocManager:
        @staticmethod
        async def append_to_updates(**kwargs):
            recorder.saved.append(kwargs.get("document_id"))

    class Log:
        @staticmethod
        def warning(*a, **k):
            return None

        @staticmethod
        def error(*a, **k):
            recorder.errors.append(" ".join(str(x) for x in a))

        info = debug = warning

    def normalize_document_id(document_id):
        return "note:" + document_id[5:] if document_id.startswith("note_") else document_id

    async def document_save_handler(document_id, data, user):
        return None

    namespace = {
        "normalize_document_id": normalize_document_id,
        "get_session_ids_from_room": lambda room: list(membership),
        "SESSION_POOL": SESSION_POOL_FOR(recorder),
        "Notes": Notes,
        "AccessGrants": AccessGrants,
        "stop_item_tasks": recorder.stop_item_tasks,
        "create_task": recorder.create_task,
        "REDIS": object(),
        "YDOC_MANAGER": YdocManager,
        "sio": Sio,
        "log": Log,
        "asyncio": asyncio,
        "document_save_handler": document_save_handler,
    }
    module = ast.Module(body=[func], type_ignores=[])
    ast.fix_missing_locations(module)
    exec(compile(module, "<patched socket/main.py>", "exec"), namespace)  # noqa: S102
    return namespace[HANDLER]


def emit(source: str, *, sid: str, document_id: str, note_owner_id: str = "owner-1",
         with_data: bool = True) -> Recorder:
    """One ydoc:document:update frame. The caller is always already in the room."""
    recorder = Recorder(note_owner_id)
    if sid not in SESSION_POOL_FOR(recorder):
        # `user = SESSION_POOL.get(sid); if not user: return` in the real
        # handler is a silent, unlogged early return, and it produces the same
        # empty cancelled/registered a working guard produces. A scenario that
        # reached it would read as a passing security assertion.
        raise SystemExit(f"FAIL: scenario sid {sid!r} is not in the session pool")
    handler = compile_handler(source, recorder, membership=[sid])
    payload = {"document_id": document_id, "update": [1, 2, 3]}
    if with_data:
        payload["data"] = {"content": "x"}
    asyncio.run(handler(sid, payload))
    return recorder


# --- the scenarios ----------------------------------------------------------


def run_leg(source: str, *, expect_leak: bool) -> None:
    seen: list[Recorder] = []

    # 1. A stranger names a victim's CHAT id. The whole issue.
    r = emit(source, sid=STRANGER_SID, document_id=VICTIM_CHAT_ID)
    seen.append(r)
    if expect_leak:
        check(
            r.cancelled == [VICTIM_CHAT_ID],
            "PRE-FIX: a stranger's ydoc update cancelled the victim chat's "
            f"in-flight tasks (observed {len(r.cancelled)} cancellation(s) on "
            "the chat keyspace); if this stops reproducing, the post-fix leg "
            "below proves nothing",
        )
        check(
            r.registered == [VICTIM_CHAT_ID],
            "PRE-FIX: it also registered a task under the victim's chat id "
            f"(observed {r.registered})",
        )
    else:
        check(
            r.cancelled == [],
            f"a stranger's ydoc update cancels NOTHING (observed {len(r.cancelled)})",
        )
        check(
            r.registered == [],
            f"and registers nothing in the chat keyspace (observed {r.registered})",
        )

    # 2. The same id in its underscore form, which normalize_document_id only
    #    rewrites for 'note_'. A chat uuid has no prefix to rewrite, so this is
    #    the same attack and must behave identically.
    r = emit(source, sid=STRANGER_SID, document_id=VICTIM_CHAT_ID.replace("-", "_"))
    seen.append(r)
    check(
        (len(r.cancelled) == 1) == expect_leak,
        "an underscore-mangled chat id behaves the same as the plain one "
        f"(observed {len(r.cancelled)} cancellation(s))",
    )

    # 2b. The other two shapes a chat id takes, both carrying a colon. These are
    #     what separates the shipped note-prefix guard from a colon-keyed one.
    for label, victim_id in (
        ("a temporary chat", VICTIM_LOCAL_ID),
        ("a channel message", VICTIM_CHANNEL_ID),
    ):
        r = emit(source, sid=STRANGER_SID, document_id=victim_id)
        seen.append(r)
        check(
            (r.cancelled == [victim_id]) == expect_leak
            and (r.registered == [victim_id]) == expect_leak,
            f"{label} id {victim_id!r} reaches the task registry only pre-fix "
            f"(cancelled={r.cancelled}, registered={r.registered})",
        )

    # 3. The note owner. The debounce must survive the fix in both directions.
    r = emit(source, sid=OWNER_SID, document_id=f"note:{NOTE_ID}")
    seen.append(r)
    check(
        r.cancelled == [f"note:{NOTE_ID}"],
        "the note owner's update still cancels the pending save for their own "
        f"note, exactly once (observed {r.cancelled})",
    )
    check(
        r.registered == [f"note:{NOTE_ID}"],
        f"and still schedules the next save (observed {r.registered})",
    )

    # 4. A stranger naming someone else's NOTE. Refused by the ownership check
    #    that already existed, in both legs; recorded so a future change to that
    #    check cannot pass unnoticed.
    r = emit(source, sid=STRANGER_SID, document_id=f"note:{NOTE_ID}")
    seen.append(r)
    check(
        r.cancelled == [] and r.registered == [],
        "a stranger's update on someone else's note touches the task registry "
        f"not at all (cancelled={r.cancelled}, registered={r.registered})",
    )

    # 5. Nothing above reached the handler's own `except Exception` arm. Without
    #    this, an empty cancelled/registered caused by an early crash is
    #    indistinguishable from one caused by the guard, and the expect_leak
    #    legs would read as clean passes.
    logged = [e for r in seen for e in r.errors]
    check(
        logged == [],
        "no scenario in this leg was swallowed by the handler's own except arm "
        f"(log.error calls: {logged})",
    )


def main() -> int:
    print("ydoc task-keyspace escape (issue #1508)")

    vendored_version, pinned_version = vendored_and_pinned_versions()
    check(
        vendored_version is not None and vendored_version == pinned_version,
        "the vendored tree and the pinned backend image are the same open-webui "
        f"version, so the patched source is the shipped source "
        f"(vendor={vendored_version}, pinned={pinned_version})",
    )

    print("\npre-fix source: socket/main.py WITHOUT the #1508 patch")
    before = patched_socket_main(apply_1508=False)
    check(
        MARKER not in before,
        f"the pre-fix source carries no {MARKER} marker, so this leg really is "
        "the code as it stood",
    )
    run_leg(before, expect_leak=True)

    print("\npost-fix source: with the #1508 patch")
    after = patched_socket_main(apply_1508=True)
    check(
        after.count(MARKER) == EXPECTED_MARKERS,
        f"the #1508 patch applied: {EXPECTED_MARKERS} {MARKER} markers "
        f"(found {after.count(MARKER)})",
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

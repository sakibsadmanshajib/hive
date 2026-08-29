#!/usr/bin/env python3
"""Structural guard on chat-deletion authorisation in the vendored chat backend.

Issue #848 recorded that automation litter could not be cleaned up, and
docs/live-test-auth.md said outright that no chat-delete route was wired up.
Both were wrong: the route exists, hard-deletes, and is already scoped to the
calling user. Measured live on 2026-08-29, a second signed-in identity was
refused (401 on read, 404 on delete) and the owner's row survived.

Since the capability was already correct, the risk this repository actually
carries is regression rather than absence. The delete handler lives in vendored
upstream source that gets bumped, and deploy/docker/owui-patches rewrites this
very file (chats.py carries seven authz markers from
apply_router_authz_family_patch.py). A bump or a patch that swapped the
user-scoped lookup for the unscoped one would hand every signed-in user the
ability to delete anyone's conversation on the shared chat instance, silently.

So this asserts the two properties that must not change:

  1. The non-admin branch of the delete handler resolves and deletes through
     the user-scoped functions, and never touches the unscoped ones.
  2. The unscoped model function is genuinely unscoped and the user-scoped one
     genuinely filters on user_id, so property 1 means what it says.

Plus one deployment property: no Caddy mutation block covers /api/v1/chats, so
the route stays reachable. That block list has grown four times (#736, #948,
#437) and a fifth entry written a little too broadly would take chat deletion
offline with a 404 that looks like an upstream fault.

Structural, no framework, no network.
Run: python3 scripts/test_owui_chat_delete_authz.py
"""

import ast
import re
import sys
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[1]
ROUTER = REPO_ROOT / "vendor/open-webui/backend/open_webui/routers/chats.py"
MODEL = REPO_ROOT / "vendor/open-webui/backend/open_webui/models/chats.py"
CADDYFILE = REPO_ROOT / "deploy/docker/Caddyfile.owui"

SCOPED_LOOKUP = "get_chat_by_id_and_user_id"
SCOPED_DELETE = "delete_chat_by_id_and_user_id"
UNSCOPED_LOOKUP = "get_chat_by_id"
UNSCOPED_DELETE = "delete_chat_by_id"

failures: list[str] = []


def check(condition: bool, message: str) -> None:
    if condition:
        print(f"  ok: {message}")
    else:
        failures.append(message)
        print(f"  FAIL: {message}")


def called_names(node: ast.AST) -> set[str]:
    """Every attribute or plain name invoked anywhere under `node`."""
    names: set[str] = set()
    for sub in ast.walk(node):
        if isinstance(sub, ast.Call):
            func = sub.func
            if isinstance(func, ast.Attribute):
                names.add(func.attr)
            elif isinstance(func, ast.Name):
                names.add(func.id)
    return names


def find_function(tree: ast.AST, name: str) -> ast.AST | None:
    for node in ast.walk(tree):
        if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef)) and node.name == name:
            return node
    return None


def admin_branch(func: ast.AST) -> ast.If | None:
    """The `if user.role == 'admin':` split inside the delete handler.

    Matched on the exact AST shape rather than on the string 'admin' appearing
    somewhere in the test. A substring match would happily accept
    `if user.role != 'admin':`, whose body is then the NON-admin path, and this
    module would go on to check the scoped calls against the wrong branch and
    pass while the boundary was inverted. It would also match an unrelated
    conditional that merely mentions the word.

    Requires, exactly: `user.role` on the left, a single `==`, and the constant
    'admin' on the right. Anything else is reported as unrecognised rather than
    guessed at, because a guess here is a false green on an authorisation
    check.
    """
    for node in ast.walk(func):
        if not isinstance(node, ast.If):
            continue
        test = node.test
        if not isinstance(test, ast.Compare):
            continue
        if len(test.ops) != 1 or not isinstance(test.ops[0], ast.Eq):
            continue
        if len(test.comparators) != 1:
            continue
        right = test.comparators[0]
        if not isinstance(right, ast.Constant) or right.value != "admin":
            continue
        left = test.left
        if not isinstance(left, ast.Attribute) or left.attr != "role":
            continue
        if not isinstance(left.value, ast.Name) or left.value.id != "user":
            continue
        return node
    return None


def route_handler(tree: ast.AST) -> ast.AST | None:
    """The handler for DELETE /{id}, distinguished from its siblings by name."""
    for node in ast.walk(tree):
        if not isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef)):
            continue
        if node.name != "delete_chat_by_id":
            continue
        for dec in node.decorator_list:
            if not isinstance(dec, ast.Call):
                continue
            f = dec.func
            if isinstance(f, ast.Attribute) and f.attr == "delete":
                if dec.args and isinstance(dec.args[0], ast.Constant):
                    if dec.args[0].value == "/{id}":
                        return node
    return None


def selfcheck() -> None:
    """The branch matcher must not accept a shape that inverts the meaning.

    Without this, the hardening in admin_branch is itself untested, and the
    substring match it replaced would pass every check in this file while the
    authorisation boundary read backwards.
    """
    correct = "async def f(user):\n    if user.role == 'admin':\n        a()\n    else:\n        b()\n"
    inverted = "async def f(user):\n    if user.role != 'admin':\n        a()\n    else:\n        b()\n"
    reversed_operands = "async def f(user):\n    if 'admin' == user.role:\n        a()\n    else:\n        b()\n"
    unrelated = "async def f(user):\n    if user.name == 'admin':\n        a()\n    else:\n        b()\n"
    mentions_only = "async def f(user):\n    if is_admin(user):\n        a()\n    else:\n        b()\n"

    cases = [
        ("the exact == shape is matched", correct, True),
        ("an inverted != test is refused", inverted, False),
        ("reversed operands are refused", reversed_operands, False),
        ("a different attribute is refused", unrelated, False),
        ("a call that merely mentions admin is refused", mentions_only, False),
    ]
    print("\nbranch matcher self-check")
    for label, source, expected in cases:
        func = ast.parse(source).body[0]
        check((admin_branch(func) is not None) == expected, label)


def main() -> int:
    print("chat delete authorisation (issues #848, #916)")
    selfcheck()

    for path in (ROUTER, MODEL, CADDYFILE):
        if not path.exists():
            print(f"  FAIL: missing {path.relative_to(REPO_ROOT)}")
            return 1

    # --- 1. The route handler's branches -------------------------------------
    print("\nrouters/chats.py DELETE /{id}")
    router_tree = ast.parse(ROUTER.read_text(encoding="utf-8"))
    handler = route_handler(router_tree)
    check(handler is not None, "the DELETE /{id} handler is present")
    if handler is None:
        return 1

    branch = admin_branch(handler)
    check(branch is not None, "the handler still splits on an admin role check")
    if branch is None:
        return 1

    non_admin = set()
    for stmt in branch.orelse:
        non_admin |= called_names(stmt)
    admin = set()
    for stmt in branch.body:
        admin |= called_names(stmt)

    check(
        SCOPED_LOOKUP in non_admin,
        f"non-admin branch resolves the row with {SCOPED_LOOKUP}",
    )
    check(
        SCOPED_DELETE in non_admin,
        f"non-admin branch deletes with {SCOPED_DELETE}",
    )
    check(
        UNSCOPED_LOOKUP not in non_admin,
        f"non-admin branch never calls the unscoped {UNSCOPED_LOOKUP}",
    )
    check(
        UNSCOPED_DELETE not in non_admin,
        f"non-admin branch never calls the unscoped {UNSCOPED_DELETE}",
    )
    check(
        UNSCOPED_DELETE in admin,
        f"the unscoped {UNSCOPED_DELETE} is reached only from the admin branch",
    )

    # --- 2. The model functions mean what their names say --------------------
    print("\nmodels/chats.py")
    model_source = MODEL.read_text(encoding="utf-8")
    model_tree = ast.parse(model_source)

    scoped = find_function(model_tree, SCOPED_DELETE)
    unscoped = find_function(model_tree, UNSCOPED_DELETE)
    check(scoped is not None, f"{SCOPED_DELETE} is defined")
    check(unscoped is not None, f"{UNSCOPED_DELETE} is defined")
    if scoped is None or unscoped is None:
        return 1

    scoped_src = ast.get_source_segment(model_source, scoped) or ""
    unscoped_src = ast.get_source_segment(model_source, unscoped) or ""

    check(
        re.search(r"delete\(Chat\)\.filter_by\(\s*id=id,\s*user_id=user_id\s*\)", scoped_src)
        is not None,
        f"{SCOPED_DELETE} filters the Chat delete on both id and user_id",
    )
    check(
        "user_id" in [a.arg for a in scoped.args.args],
        f"{SCOPED_DELETE} still takes a user_id argument",
    )
    check(
        "user_id" not in [a.arg for a in unscoped.args.args],
        f"{UNSCOPED_DELETE} takes no user_id, so it must stay admin only",
    )
    # A soft delete would leave the row readable; the product deletes for real,
    # and `archived` is the separate, deliberate soft path.
    check(
        "delete(Chat)" in scoped_src and "delete(ChatMessage)" in scoped_src,
        f"{SCOPED_DELETE} removes the chat row and its messages rather than flagging them",
    )

    # --- 3. Deployment: the route is not blocked at the edge ------------------
    print("\nCaddyfile.owui")
    lines = CADDYFILE.read_text(encoding="utf-8").splitlines()
    offending: list[str] = []
    in_mutation_block = False
    for line in lines:
        stripped = line.strip()
        if stripped.startswith("#"):
            continue
        if re.match(r"^method\b.*\bDELETE\b", stripped):
            in_mutation_block = True
            continue
        if in_mutation_block:
            if stripped.startswith("path"):
                if "chats" in stripped:
                    offending.append(stripped)
            elif stripped in ("}", ""):
                in_mutation_block = False
    check(
        not offending,
        "no method block that includes DELETE matches a chats path "
        f"(found: {offending})",
    )

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

#!/usr/bin/env python3
"""Structural guard on chat-deletion authorisation in the chat backend we ship.

Issue #848 recorded that automation litter could not be cleaned up, and
docs/live-test-auth.md said outright that no chat-delete route was wired up.
That was wrong for chats: the route exists and hard-deletes. Measured live on
2026-08-29, a second signed-in non-admin identity was refused (401 on read, 404
on delete) and the owner's row survived.

Since the capability was already correct, the risk this repository carries is
regression rather than absence. The delete handler lives in vendored upstream
source that gets bumped, and deploy/docker/owui-patches rewrites this very file
(chats.py carries 7 markers from apply_router_authz_family_patch.py and 1 from
apply_authz_residuals_1191_patch.py). A bump or a patch that widened the
scoping would hand every signed-in user the ability to delete anyone's
conversation on the shared chat instance, silently.

WHAT THIS ASSERTS ON, AND WHY IT IS NOT THE VENDORED FILE
---------------------------------------------------------
deploy/docker/Dockerfile.open-webui builds only the frontend from
vendor/open-webui and keeps the pinned upstream image for the Python backend,
then rewrites routers/chats.py inside that image at build time. Reading the
pre-patch vendored copy would therefore be blind to a patch that widened the
scoping, which is exactly the risk named above: an upstream-bump detector
wearing an authorisation-guard docstring. So this runs the real patch scripts,
in the Dockerfile's order, against copies of the vendored source and asserts on
the patched result. Same harness as scripts/test_owui_knowledge_authz.py.

THE PROPERTIES PINNED
---------------------
Handler (routers/chats.py, DELETE /{id}), on patched source:

  1. It still splits on `user.role == 'admin'`.
  2. The non-admin arm gates on the `chat.delete` permission before it does
     anything else. Deletion for an ordinary user is configuration dependent,
     not unconditional, and a toggle or a patched default silently returns the
     product to the state the old document described.
  3. The non-admin arm resolves the row with the user-scoped lookup, then
     raises a 404 when that lookup came back falsy, and only then calls the
     user-scoped delete. The ORDER is the property. Names alone are not: a bump
     that keeps both scoped names and drops the 404 between them passes a
     name-presence check while letting a non-owner destroy another user's
     messages (see the model note below).
  4. The non-admin arm never reaches the unscoped lookup or delete, and the
     unscoped delete is reachable only from the admin arm.

Model (models/chats.py), which no owui-patch touches, so the vendored copy is
the shipped copy:

  5. delete_chat_by_id_and_user_id is user scoped in its name and in exactly
     one of its writes. The Chat delete carries user_id; the ChatMessage delete
     and the AutomationRun update are keyed on chat_id alone, and
     delete_shared_chat_by_chat_id is unscoped by name. That is safe only
     because of property 3: the handler has already refused a non-owner. This
     file records that coupling rather than implying a scoping that is not
     there, and goes red if a further write appears or an existing one loses
     its key.
  6. The unscoped delete takes no user_id, so it must stay admin only.

Deployment (deploy/docker/Caddyfile.owui):

  7. No 4xx or 5xx `respond` in front of the proxy answers
     `DELETE /api/v1/chats/<uuid>`. The check evaluates each matcher against
     that concrete request rather than scanning block structure, because the
     block that has actually grown (#736, #769, #770, #771, #947, #948, #437)
     is `@blocked`, a bare path_regexp with no `method` line at all: a `chats`
     arm added there would 404 the delete route for every verb, and a
     structure scanner armed by a `method` line would never look at it.

  8. The unscoped admin arm is reachable only when ENABLE_ADMIN_CHAT_ACCESS is
     set, and this deployment sets it to "false" in docker-compose.yml. Both
     halves are pinned, because both are load bearing and neither is visible in
     the pre-patch vendored file: upstream lets any admin delete any user's
     conversation, and on this deployment that would not be a narrow class,
     since every tenant OWNER holds an administrator session (#748, #948).

WHAT THIS DOES NOT COVER
------------------------
`stop_item_tasks` runs at the top of the handler, before the admin split and
before any ownership resolution, so a verified non-owner who holds a chat id
can cancel that chat's in-flight completion by issuing a DELETE they are then
refused. Filed as issue #1474; the 404 this file pins is the delete boundary,
not that one.

Structural, no framework, no network.
Run: python3 scripts/test_owui_chat_delete_authz.py
"""

import ast
import os
import re
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[1]
VENDORED_ROUTERS = REPO_ROOT / "vendor/open-webui/backend/open_webui/routers"
MODEL = REPO_ROOT / "vendor/open-webui/backend/open_webui/models/chats.py"
PATCHES = REPO_ROOT / "deploy/docker/owui-patches"
CADDYFILE = REPO_ROOT / "deploy/docker/Caddyfile.owui"
COMPOSE = REPO_ROOT / "deploy/docker/docker-compose.yml"

# apply_router_authz_family_patch.py rewrites every file in this set and fails
# if one is missing, so the whole family is copied even though only chats.py is
# read afterwards. Mirrors EXPECTED_MARKERS in that script.
FAMILY_ROUTERS = (
    "knowledge.py",
    "files.py",
    "evaluations.py",
    "folders.py",
    "calendar.py",
    "chats.py",
    "prompts.py",
    "notes.py",
    "tools.py",
    "models.py",
)

SCOPED_LOOKUP = "get_chat_by_id_and_user_id"
SCOPED_DELETE = "delete_chat_by_id_and_user_id"
UNSCOPED_LOOKUP = "get_chat_by_id"
UNSCOPED_DELETE = "delete_chat_by_id"
DELETE_PERMISSION = "chat.delete"
ADMIN_ACCESS_FLAG = "ENABLE_ADMIN_CHAT_ACCESS"

# The concrete request the deployment check is about. A real chat id, shaped
# like the ones the capture recorded, so a matcher is evaluated rather than
# grepped for a literal.
DELETE_REQUEST_PATH = "/api/v1/chats/e85bb8ac-32f1-4bcb-a5af-2c56060ce571"
DELETE_REQUEST_METHOD = "DELETE"

failures: list[str] = []


def check(condition: bool, message: str) -> None:
    if condition:
        print(f"  ok: {message}")
    else:
        failures.append(message)
        print(f"  FAIL: {message}")


# --- shipped source ---------------------------------------------------------


def patched_chats_router() -> str:
    """routers/chats.py as the image runs it, not as the vendor ships it.

    Runs the two build-time patches that touch this file, in the order
    Dockerfile.open-webui runs them (family #1186, then residuals #1191). A
    patch failure is fatal here rather than silently falling back to the
    vendored text, because a fallback would turn this whole module back into
    the pre-patch check it replaced.
    """
    tmp = Path(tempfile.mkdtemp(prefix="owui-chat-authz-"))
    for name in FAMILY_ROUTERS:
        shutil.copy(VENDORED_ROUTERS / name, tmp / name)
    env = dict(os.environ)
    env["HIVE_OWUI_ROUTERS_DIR"] = str(tmp)
    for patch in (
        "apply_router_authz_family_patch.py",
        "apply_authz_residuals_1191_patch.py",
    ):
        result = subprocess.run(
            [sys.executable, str(PATCHES / patch)],
            env=env,
            capture_output=True,
            text=True,
        )
        if result.returncode != 0:
            raise SystemExit(f"FAIL: {patch} failed:\n{result.stdout}{result.stderr}")
    return (tmp / "chats.py").read_text(encoding="utf-8")


# --- AST helpers ------------------------------------------------------------


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


def is_admin_role_test(test: ast.AST) -> bool:
    """Exactly `user.role == 'admin'`, and nothing that merely resembles it."""
    if not isinstance(test, ast.Compare):
        return False
    if len(test.ops) != 1 or not isinstance(test.ops[0], ast.Eq):
        return False
    if len(test.comparators) != 1:
        return False
    right = test.comparators[0]
    if not isinstance(right, ast.Constant) or right.value != "admin":
        return False
    left = test.left
    if not isinstance(left, ast.Attribute) or left.attr != "role":
        return False
    return isinstance(left.value, ast.Name) and left.value.id == "user"


def admin_branch(func: ast.AST) -> ast.If | None:
    """The admin split inside the delete handler, patched or not.

    Two shapes are legal. Upstream ships `if user.role == 'admin':`; after
    apply_router_authz_family_patch.py the same branch reads
    `if user.role == 'admin' and ENABLE_ADMIN_CHAT_ACCESS:` (#1186). Both put
    the unscoped calls in the body and the scoped ones in the else, so both are
    accepted, and the extra conjunct is reported separately by
    `admin_branch_flags` so it can be pinned rather than merely tolerated.

    Matched on AST shape rather than on the string 'admin' appearing somewhere
    in the test. A substring match would happily accept
    `if user.role != 'admin':`, whose body is then the NON-admin path, and this
    module would go on to check the scoped calls against the wrong branch and
    pass while the boundary was inverted.

    `or` is refused as firmly as `!=`. An `and` can only narrow who reaches the
    unscoped arm; an `or` widens it, which is the same inversion wearing a
    different operator.
    """
    for node in ast.walk(func):
        if not isinstance(node, ast.If):
            continue
        test = node.test
        if is_admin_role_test(test):
            return node
        if isinstance(test, ast.BoolOp) and isinstance(test.op, ast.And):
            if any(is_admin_role_test(value) for value in test.values):
                return node
    return None


def admin_branch_flags(node: ast.If) -> set[str]:
    """The names conjoined with the role test, if the branch was flag-gated."""
    test = node.test
    if not isinstance(test, ast.BoolOp) or not isinstance(test.op, ast.And):
        return set()
    return {
        value.id
        for value in test.values
        if isinstance(value, ast.Name) and not is_admin_role_test(value)
    }


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


def assigns_from(stmt: ast.stmt, callee: str) -> str | None:
    """Name bound by `x = ... callee(...) ...`, if this statement is one."""
    if not isinstance(stmt, ast.Assign) or len(stmt.targets) != 1:
        return None
    target = stmt.targets[0]
    if not isinstance(target, ast.Name):
        return None
    return target.id if callee in called_names(stmt.value) else None


def raises_http_error(stmt: ast.stmt, detail_name: str) -> bool:
    """`raise HTTPException(...)` under `stmt`, carrying the named error detail.

    Matched on the ERROR_MESSAGES member rather than on a bare status literal,
    because upstream writes the status as `status.HTTP_404_NOT_FOUND` and a
    numeric search would miss it while a bare `ast.dump` substring search would
    also match an unrelated 404 elsewhere in the same statement.
    """
    for sub in ast.walk(stmt):
        if not isinstance(sub, ast.Raise) or sub.exc is None:
            continue
        exc = sub.exc
        if not isinstance(exc, ast.Call):
            continue
        name = exc.func.attr if isinstance(exc.func, ast.Attribute) else getattr(exc.func, "id", "")
        if name != "HTTPException":
            continue
        attrs = {
            node.attr for node in ast.walk(exc) if isinstance(node, ast.Attribute)
        }
        if detail_name in attrs:
            return True
    return False


def guards_falsy(stmt: ast.stmt, name: str) -> bool:
    """`if not <name>:` whose body raises the not-found error."""
    if not isinstance(stmt, ast.If):
        return False
    test = stmt.test
    if not isinstance(test, ast.UnaryOp) or not isinstance(test.op, ast.Not):
        return False
    operand = test.operand
    if not isinstance(operand, ast.Name) or operand.id != name:
        return False
    return any(raises_http_error(s, "NOT_FOUND") for s in stmt.body)


def permission_gate(stmts: list[ast.stmt], permission: str) -> int | None:
    """Index of the statement that raises unless `permission` is held."""
    for index, stmt in enumerate(stmts):
        if "has_permission" not in called_names(stmt):
            continue
        constants = {
            node.value
            for node in ast.walk(stmt)
            if isinstance(node, ast.Constant) and isinstance(node.value, str)
        }
        if permission in constants and raises_http_error(stmt, "ACCESS_PROHIBITED"):
            return index
    return None


def statement_index(stmts: list[ast.stmt], callee: str) -> int | None:
    for index, stmt in enumerate(stmts):
        if callee in called_names(stmt):
            return index
    return None


# --- model writes -----------------------------------------------------------


def session_writes(func: ast.AST) -> list[tuple[str, str, tuple[str, ...]]]:
    """Every `delete(X)/update(X).filter_by(...)` handed to session.execute.

    Returns (verb, table, filter keys). A write expressed through a shape this
    does not recognise comes back as ("unrecognised", "?", ()), so a new one
    cannot slip past by being written differently: the equality assertion in
    main() goes red either way.
    """
    writes: list[tuple[str, str, tuple[str, ...]]] = []
    for node in ast.walk(func):
        if not isinstance(node, ast.Call):
            continue
        callee = node.func
        if not isinstance(callee, ast.Attribute) or callee.attr != "execute":
            continue
        if not node.args:
            continue
        keys: tuple[str, ...] = ()
        verb = "unrecognised"
        table = "?"
        cursor = node.args[0]
        while isinstance(cursor, ast.Call) and isinstance(cursor.func, ast.Attribute):
            if cursor.func.attr == "filter_by":
                keys = tuple(sorted(k.arg or "" for k in cursor.keywords))
            cursor = cursor.func.value
        if isinstance(cursor, ast.Call) and isinstance(cursor.func, ast.Name):
            verb = cursor.func.id
            if cursor.args and isinstance(cursor.args[0], ast.Name):
                table = cursor.args[0].id
        writes.append((verb, table, keys))
    return writes


# --- Caddy ------------------------------------------------------------------


def glob_to_regex(pattern: str) -> str:
    """Caddy `path` glob to a regex. `*` spans separators, as Caddy's does."""
    return "".join(".*" if ch == "*" else re.escape(ch) for ch in pattern)


def path_expr_matches(directive: str, args: list[str], target: str) -> bool:
    if directive == "path":
        # Caddy matches paths case-insensitively and any listed pattern wins.
        return any(re.fullmatch(glob_to_regex(a), target, re.IGNORECASE) for a in args)
    if directive == "path_regexp":
        # An optional capture-group name may precede the expression.
        expr = args[-1] if args else ""
        try:
            return re.search(expr, target) is not None
        except re.error:
            # An expression this parser cannot compile is reported as matching,
            # so an unreadable matcher fails loudly instead of passing quietly.
            return True
    return False


def matcher_matches(directives: list[tuple[str, list[str]]], path: str, method: str) -> bool:
    """Caddy ANDs every directive in a matcher block. No directive means match.

    Order independent on purpose: Caddyfile matcher bodies have no required
    order, and the loop this replaced only armed when a `method` line came
    first. A block with no `method` line at all matches every verb, which is
    the shape of `@blocked` and `@removedSurfaces`.
    """
    for directive, args in directives:
        negated = directive.startswith("not ")
        name = directive[4:] if negated else directive
        if name == "method":
            result = method.upper() in {a.upper() for a in args}
        elif name in ("path", "path_regexp"):
            result = path_expr_matches(name, args, path)
        else:
            # An unknown directive could narrow the match, so treat the block as
            # non-matching rather than claim an outage that may not exist.
            return False
        if negated:
            result = not result
        if not result:
            return False
    return True


def parse_caddy(text: str):
    """Named matchers, the `respond` lines using them, and enclosing scope.

    Returns (matchers, responds, enclosing) where `enclosing` maps a matcher
    name to the path patterns of any `handle`/`route` block it sits inside, so
    scoping expressed by the enclosing directive rather than by a `path` line
    is not invisible.
    """
    matchers: dict[str, list[tuple[str, list[str]]]] = {}
    responds: list[tuple[str, str]] = []
    enclosing: dict[str, list[str]] = {}
    enclosing_stack: list[list[str]] = []
    current: str | None = None
    for raw in text.splitlines():
        stripped = raw.strip()
        if stripped.startswith("#"):
            continue
        if not stripped:
            continue
        if current is not None:
            if stripped.startswith("}"):
                current = None
                continue
            parts = stripped.split()
            if parts[0] == "not" and len(parts) > 1:
                matchers[current].append(("not " + parts[1], parts[2:]))
            else:
                matchers[current].append((parts[0], parts[1:]))
            continue
        parts = stripped.split()
        if parts[0].startswith("@"):
            name = parts[0]
            enclosing[name] = [p for frame in enclosing_stack for p in frame]
            if stripped.endswith("{"):
                matchers[name] = []
                current = name
            else:
                # One-line form: `@name path /a /b`
                body = parts[1:]
                if body:
                    matchers[name] = [(body[0], body[1:])]
            continue
        if parts[0] in ("handle", "route") and stripped.endswith("{"):
            enclosing_stack.append([p for p in parts[1:] if p != "{"])
            continue
        if parts[0] == "respond" and len(parts) >= 3 and parts[1].startswith("@"):
            responds.append((parts[1], parts[2]))
            continue
        if stripped.startswith("}") and enclosing_stack:
            enclosing_stack.pop()
    return matchers, responds, enclosing


def blocking_responds(text: str, path: str, method: str) -> list[str]:
    """Every `respond @x <4xx/5xx>` that answers this request instead of the app."""
    matchers, responds, enclosing = parse_caddy(text)
    offending: list[str] = []
    for name, status in responds:
        if not status.isdigit() or int(status) < 400:
            continue
        directives = matchers.get(name)
        if directives is None:
            continue
        scope = enclosing.get(name) or []
        if scope and not any(
            re.fullmatch(glob_to_regex(p), path, re.IGNORECASE) for p in scope
        ):
            continue
        if matcher_matches(directives, path, method):
            offending.append(f"{name} -> {status}")
    return offending


# --- self-checks ------------------------------------------------------------


def selfcheck_branch_matcher() -> None:
    """The branch matcher must not accept a shape that inverts the meaning."""
    cases = [
        ("the exact == shape is matched", "if user.role == 'admin':", True),
        (
            "the patched `and ENABLE_ADMIN_CHAT_ACCESS` shape is matched",
            "if user.role == 'admin' and ENABLE_ADMIN_CHAT_ACCESS:",
            True,
        ),
        (
            "an `or` conjunct is refused, since it widens the unscoped arm",
            "if user.role == 'admin' or ENABLE_ADMIN_CHAT_ACCESS:",
            False,
        ),
        ("an inverted != test is refused", "if user.role != 'admin':", False),
        ("reversed operands are refused", "if 'admin' == user.role:", False),
        ("a different attribute is refused", "if user.name == 'admin':", False),
        ("a call that merely mentions admin is refused", "if is_admin(user):", False),
    ]
    print("\nbranch matcher self-check")
    for label, test, expected in cases:
        source = f"async def f(user):\n    {test}\n        a()\n    else:\n        b()\n"
        func = ast.parse(source).body[0]
        check((admin_branch(func) is not None) == expected, label)


def selfcheck_order_matcher() -> None:
    """The ordering check must go red on the mutants that keep both names.

    Both of these leave `get_chat_by_id_and_user_id` and
    `delete_chat_by_id_and_user_id` in place, so a name-presence check passes
    on them while a non-owner destroys another user's messages.
    """
    prelude = (
        "async def f(user, id):\n"
        "    if user.role == 'admin':\n"
        "        pass\n"
        "    else:\n"
    )
    gate = (
        "        if not await has_permission(user.id, 'chat.delete', p):\n"
        "            raise HTTPException(status_code=s.HTTP_401_UNAUTHORIZED, "
        "detail=ERROR_MESSAGES.ACCESS_PROHIBITED)\n"
    )
    lookup = "        chat = await Chats.get_chat_by_id_and_user_id(id, user.id)\n"
    guard = (
        "        if not chat:\n"
        "            raise HTTPException(status_code=s.HTTP_404_NOT_FOUND, "
        "detail=ERROR_MESSAGES.NOT_FOUND)\n"
    )
    delete = "        return await Chats.delete_chat_by_id_and_user_id(id, user.id)\n"

    cases = [
        ("the correct order is accepted", prelude + gate + lookup + guard + delete, True),
        ("the 404 removed is refused", prelude + gate + lookup + delete, False),
        ("the permission gate removed is refused", prelude + lookup + guard + delete, False),
        ("the delete moved above the 404 is refused", prelude + gate + lookup + delete + guard, False),
    ]
    print("\nhandler order self-check")
    for label, source, expected in cases:
        branch = admin_branch(ast.parse(source).body[0])
        stmts = list(branch.orelse) if branch else []
        permission_at = permission_gate(stmts, DELETE_PERMISSION)
        lookup_at = None
        resolved = None
        for index, stmt in enumerate(stmts):
            found = assigns_from(stmt, SCOPED_LOOKUP)
            if found is not None:
                lookup_at, resolved = index, found
                break
        guard_at = None
        if resolved is not None:
            for index, stmt in enumerate(stmts):
                if guards_falsy(stmt, resolved):
                    guard_at = index
                    break
        delete_at = statement_index(stmts, SCOPED_DELETE)
        ordered = None not in (permission_at, lookup_at, guard_at, delete_at) and (
            permission_at < lookup_at < guard_at < delete_at
        )
        check(ordered == expected, label)


def selfcheck_caddy_matcher() -> None:
    """The shapes the line scanner this replaced could not see.

    Each was run as a real mutant against the previous implementation and each
    returned exit 0 while taking chat deletion offline. They are negative
    controls now: if this evaluator stops catching one, it has regressed to a
    structure scanner and the deployment claim is worthless again.
    """
    control = (
        "site {\n  @m {\n    method PUT POST PATCH DELETE\n"
        "    path /api/v*/chats/*\n  }\n  respond @m 404\n}\n"
    )
    reordered = (
        "site {\n  @m {\n    path /api/v*/chats/*\n"
        "    method PUT POST PATCH DELETE\n  }\n  respond @m 404\n}\n"
    )
    no_method = (
        "site {\n  @blocked {\n"
        "    path_regexp blocked (?i)^/+api/v[0-9]+/+chats(?:/.*)?$\n"
        "  }\n  respond @blocked 404\n}\n"
    )
    spelled_differently = (
        "site {\n  @m {\n"
        "    path_regexp mut (?i)^/+api/v[0-9]+/+chat(?:s)?(?:/.*)?$\n"
        "  }\n  respond @m 404\n}\n"
    )
    enclosing_scope = (
        "site {\n  handle /api/v1/chats/* {\n    @m {\n      method DELETE\n"
        "    }\n    respond @m 404\n  }\n}\n"
    )
    unaffected = (
        "site {\n  @adminMutation {\n    method PUT POST PATCH DELETE\n"
        "    path /api/v*/users/* /api/v*/groups* /api/v*/models*\n  }\n"
        "  respond @adminMutation 404\n"
        "  @ok {\n    method DELETE\n    path /api/v*/chats/*\n  }\n"
        "  respond @ok 204\n}\n"
    )
    other_verb_only = (
        "site {\n  @m {\n    method POST\n    path /api/v*/chats/*\n  }\n"
        "  respond @m 404\n}\n"
    )
    cases = [
        ("a plain method-then-path block is caught (control)", control, True),
        ("a path-then-method block is caught (order independence)", reordered, True),
        ("a block with no method line is caught (@blocked's shape)", no_method, True),
        ("a regexp that never spells 'chats' is caught", spelled_differently, True),
        ("scoping by an enclosing handle is caught", enclosing_scope, True),
        ("an unrelated path, and a non-4xx respond, are not flagged", unaffected, False),
        ("a block that blocks another verb is not flagged", other_verb_only, False),
    ]
    print("\nCaddy matcher self-check")
    for label, text, expected in cases:
        hit = bool(blocking_responds(text, DELETE_REQUEST_PATH, DELETE_REQUEST_METHOD))
        check(hit == expected, label)


# --- main -------------------------------------------------------------------


def main() -> int:
    print("chat delete authorisation (issues #848, #916)")
    selfcheck_branch_matcher()
    selfcheck_order_matcher()
    selfcheck_caddy_matcher()

    for path in (VENDORED_ROUTERS / "chats.py", MODEL, CADDYFILE, COMPOSE):
        if not path.exists():
            print(f"  FAIL: missing {path.relative_to(REPO_ROOT)}")
            return 1

    # --- 1. The route handler, as the image runs it --------------------------
    print("\nrouters/chats.py DELETE /{id} (after owui-patches)")
    handler = route_handler(ast.parse(patched_chats_router()))
    check(handler is not None, "the DELETE /{id} handler is present in the patched source")
    if handler is None:
        return 1

    branch = admin_branch(handler)
    check(branch is not None, "the handler still splits on an admin role check")
    if branch is None:
        return 1

    non_admin_stmts = list(branch.orelse)
    non_admin = set()
    for stmt in non_admin_stmts:
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

    # Who can reach that unscoped arm at all. Upstream lets any admin in, and on
    # this deployment that is not a narrow class: every tenant OWNER holds an
    # administrator session (#748, #948). What closes it is the pair below, and
    # both halves have to hold, so both are pinned. Reading the pre-patch
    # vendored file would see neither.
    check(
        ADMIN_ACCESS_FLAG in admin_branch_flags(branch),
        f"the unscoped admin arm is gated on {ADMIN_ACCESS_FLAG} by "
        "apply_router_authz_family_patch.py (#1186)",
    )
    compose = COMPOSE.read_text(encoding="utf-8")
    check(
        re.search(rf'^\s*{ADMIN_ACCESS_FLAG}:\s*"false"\s*$', compose, re.MULTILINE)
        is not None,
        f"{ADMIN_ACCESS_FLAG} is \"false\" in docker-compose.yml, so on this "
        "deployment the unscoped arm is unreachable and every caller, admin "
        "included, deletes through the user-scoped path",
    )

    # The ownership decision itself, which the call names do not carry. A bump
    # that keeps both scoped names and drops the 404 between them leaves the
    # non-owner's messages deletable (see the model section), and every check
    # above would still be green.
    permission_at = permission_gate(non_admin_stmts, DELETE_PERMISSION)
    check(
        permission_at is not None,
        f"non-admin branch refuses a caller without the '{DELETE_PERMISSION}' permission",
    )

    lookup_at = None
    resolved_name = None
    for index, stmt in enumerate(non_admin_stmts):
        name = assigns_from(stmt, SCOPED_LOOKUP)
        if name is not None:
            lookup_at, resolved_name = index, name
            break
    check(
        lookup_at is not None,
        f"the {SCOPED_LOOKUP} result is bound to a name the branch can test",
    )

    guard_at = None
    if resolved_name is not None:
        for index, stmt in enumerate(non_admin_stmts):
            if guards_falsy(stmt, resolved_name):
                guard_at = index
                break
    check(
        guard_at is not None,
        f"a 404 is raised when {SCOPED_LOOKUP} returns nothing, so a non-owner is refused",
    )

    delete_at = statement_index(non_admin_stmts, SCOPED_DELETE)
    check(delete_at is not None, f"the branch reaches {SCOPED_DELETE}")

    if None not in (permission_at, lookup_at, guard_at, delete_at):
        check(
            permission_at < lookup_at < guard_at < delete_at,
            "the order holds: permission gate, then scoped lookup, then the 404, "
            "then the delete, so the delete is unreachable for a row the caller "
            "does not own",
        )

    # --- 2. The model functions mean what their names say --------------------
    # No owui-patch rewrites models/chats.py, so the vendored copy is the
    # shipped copy. Checked rather than assumed:
    print("\nmodels/chats.py")
    patch_sources = "\n".join(
        p.read_text(encoding="utf-8") for p in sorted(PATCHES.glob("*.py"))
    )
    check(
        "models/chats.py" not in patch_sources,
        "no owui-patch rewrites models/chats.py, so the vendored copy is the shipped copy",
    )

    model_source = MODEL.read_text(encoding="utf-8")
    model_tree = ast.parse(model_source)

    scoped = find_function(model_tree, SCOPED_DELETE)
    unscoped = find_function(model_tree, UNSCOPED_DELETE)
    check(scoped is not None, f"{SCOPED_DELETE} is defined")
    check(unscoped is not None, f"{UNSCOPED_DELETE} is defined")
    if scoped is None or unscoped is None:
        return 1

    check(
        "user_id" in [a.arg for a in scoped.args.args],
        f"{SCOPED_DELETE} still takes a user_id argument",
    )
    check(
        "user_id" not in [a.arg for a in unscoped.args.args],
        f"{UNSCOPED_DELETE} takes no user_id, so it must stay admin only",
    )

    # The honest shape of this function, pinned exactly. It is user scoped in
    # its name and in one of its writes; the others are keyed on the chat id
    # alone, and the safety of those lives in the handler's 404 above, not
    # here. Pinning the whole set means a further write, or an existing one
    # losing its key, goes red instead of hiding behind the function's name.
    writes = session_writes(scoped)
    check(
        writes
        == [
            ("update", "AutomationRun", ("chat_id",)),
            ("delete", "ChatMessage", ("chat_id",)),
            ("delete", "Chat", ("id", "user_id")),
        ],
        f"{SCOPED_DELETE} makes exactly the three known session writes, and only "
        f"the Chat delete carries user_id (found: {writes})",
    )
    check(
        "delete_shared_chat_by_chat_id" in called_names(scoped),
        f"{SCOPED_DELETE} also clears the shared-chat snapshot, which is keyed on "
        "chat_id alone; like the two writes above it is safe only because the "
        "handler refused a non-owner first",
    )
    # A soft delete would leave the row readable; the product deletes for real,
    # and `archived` is the separate, deliberate soft path.
    check(
        ("delete", "Chat", ("id", "user_id")) in writes
        and ("delete", "ChatMessage", ("chat_id",)) in writes,
        f"{SCOPED_DELETE} removes the chat row and its messages rather than flagging them",
    )

    # --- 3. Deployment: the route is not blocked at the edge ------------------
    print("\nCaddyfile.owui")
    offending = blocking_responds(
        CADDYFILE.read_text(encoding="utf-8"),
        DELETE_REQUEST_PATH,
        DELETE_REQUEST_METHOD,
    )
    check(
        not offending,
        f"no matcher answers {DELETE_REQUEST_METHOD} {DELETE_REQUEST_PATH} with a "
        f"4xx or 5xx before the proxy (found: {offending})",
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

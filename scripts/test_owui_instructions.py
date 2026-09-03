#!/usr/bin/env python3
"""The custom-instructions shim and its build-time splice (issue #1363).

Two halves, both structural: no container, no network, no FastAPI.

SPLICE. apply_instructions_patch.py is driven against a synthesized main.py
through HIVE_OWUI_MAIN_PATH. Asserted: it lands exactly once, the result still
parses, a second run is a no-op rather than a second mount, and a main.py whose
anchor has moved fails LOUDLY instead of writing an image whose settings pane
404s on save. That last one is the whole reason this file exists: the failure
it guards against is silent at build time and only visible to a customer.

SHIM. hive_instructions.py is read as an AST rather than as text, because the
properties worth pinning are structural and a string match survives the
refactor that breaks them:

  * both routes take their principal from `Depends(get_verified_user)` and from
    nothing else, so no caller-supplied identity can reach the upstream call;
  * the forwarded body is rebuilt from the single `content` field rather than
    passed through, so nothing else a caller invents is forwarded;
  * the write route bounds the body BEFORE forwarding it;
  * the upstream path is the fixed constant, never built from request data;
  * no error message returned to the browser interpolates anything, which is
    how the shim key, the user token, and the upstream URL stay out of it.

Run: python3 scripts/test_owui_instructions.py
"""

from __future__ import annotations

import ast
import os
import pathlib
import subprocess
import sys
import tempfile

ROOT = pathlib.Path(__file__).resolve().parent.parent
PATCH = ROOT / 'deploy' / 'docker' / 'owui-patches' / 'apply_instructions_patch.py'
SHIM = ROOT / 'deploy' / 'docker' / 'owui-patches' / 'hive_instructions.py'

ANCHOR_LINE = "app.include_router(utils.router, prefix='/api/v1/utils', tags=['utils'])"

MAIN_STUB = f"""from fastapi import FastAPI

app = FastAPI()

app.include_router(chats.router, prefix='/api/v1/chats', tags=['chats'])
{ANCHOR_LINE}


@app.get('/health')
def health():
    return {{'status': True}}
"""

failures: list[str] = []


def check(condition: bool, message: str) -> None:
    if condition:
        print(f'  ok: {message}')
    else:
        print(f'  FAIL: {message}')
        failures.append(message)


def run_patch(main_path: pathlib.Path) -> subprocess.CompletedProcess:
    env = dict(os.environ, HIVE_OWUI_MAIN_PATH=str(main_path))
    return subprocess.run(
        [sys.executable, str(PATCH)], env=env, capture_output=True, text=True
    )


def test_splice() -> None:
    print('splice:')
    with tempfile.TemporaryDirectory() as tmp:
        main_path = pathlib.Path(tmp) / 'main.py'
        main_path.write_text(MAIN_STUB)

        first = run_patch(main_path)
        check(first.returncode == 0, f'first run succeeds (stderr: {first.stderr.strip()})')

        patched = main_path.read_text()
        check(
            patched.count('hive_instructions_router') == 2,
            'the router is imported once and included once',
        )
        check(
            "prefix='/api/v1/hive/instructions'" in patched,
            'the routes are mounted on /api/v1/hive/instructions',
        )
        try:
            ast.parse(patched)
            check(True, 'the patched main.py parses')
        except SyntaxError as exc:
            check(False, f'the patched main.py parses ({exc})')

        # Idempotence matters because the image build is not the only thing
        # that runs this: a rebuilt layer, or a rerun during debugging, must
        # not mount the router twice and shadow the first include.
        second = run_patch(main_path)
        check(second.returncode == 0, 'a second run succeeds')
        check(
            main_path.read_text() == patched,
            'a second run changes nothing',
        )

    with tempfile.TemporaryDirectory() as tmp:
        moved = pathlib.Path(tmp) / 'main.py'
        moved.write_text(MAIN_STUB.replace(ANCHOR_LINE, '# upstream moved this'))
        result = run_patch(moved)
        check(
            result.returncode != 0,
            'a main.py whose anchor has moved fails the build rather than shipping unmounted',
        )
        check(
            'hive_instructions_router' not in moved.read_text(),
            'a failed splice writes nothing',
        )


def shim_tree() -> ast.Module:
    return ast.parse(SHIM.read_text())


def route_functions(tree: ast.Module) -> dict[str, ast.AsyncFunctionDef]:
    out: dict[str, ast.AsyncFunctionDef] = {}
    for node in tree.body:
        if not isinstance(node, ast.AsyncFunctionDef):
            continue
        for dec in node.decorator_list:
            src = ast.unparse(dec)
            if src.startswith('router.'):
                out[node.name] = node
    return out


def test_shim() -> None:
    print('shim:')
    tree = shim_tree()
    routes = route_functions(tree)
    check(
        set(routes) == {'read_instructions', 'write_instructions'},
        f'exactly two routes are exposed (found {sorted(routes)})',
    )

    for name, fn in sorted(routes.items()):
        defaults = [ast.unparse(d) for d in fn.args.defaults]
        check(
            defaults == ['Depends(get_verified_user)'],
            f'{name} takes its principal only from the verified session',
        )
        params = [a.arg for a in fn.args.args]
        for forbidden in ('user_id', 'tenant_id', 'email', 'token'):
            check(
                forbidden not in params,
                f'{name} accepts no {forbidden} parameter',
            )

    write = routes.get('write_instructions')
    if write is not None:
        body = ast.unparse(write)
        check(
            "{'content': content}" in body,
            'the forwarded body is rebuilt from the content field alone',
        )
        # Order matters: a size check after the forward is not a size check.
        forward_at = body.index("_call(request, user, 'PUT'")
        bound_at = body.index('MAX_REQUEST_BODY_BYTES')
        check(bound_at < forward_at, 'the body is bounded before it is forwarded')

    call = next(
        (n for n in tree.body if isinstance(n, ast.AsyncFunctionDef) and n.name == '_call'),
        None,
    )
    check(call is not None, 'the single upstream call helper exists')
    if call is not None:
        src = ast.unparse(call)
        check(
            "f'{base}{UPSTREAM_PATH}'" in src,
            'the upstream URL is the fixed constant, never built from request data',
        )

    # Every message the browser can see must be a plain literal. An f-string
    # here is how a credential or an internal URL reaches a customer.
    for node in ast.walk(tree):
        if not isinstance(node, ast.Call):
            continue
        if not (isinstance(node.func, ast.Name) and node.func.id == 'HTTPException'):
            continue
        for kw in node.keywords:
            if kw.arg != 'detail':
                continue
            check(
                isinstance(kw.value, ast.Constant) and isinstance(kw.value.value, str),
                f'the refusal message {ast.unparse(kw.value)!r} is a plain literal',
            )


def main() -> int:
    test_splice()
    test_shim()
    print()
    if failures:
        print(f'{len(failures)} check(s) failed')
        return 1
    print('all checks passed')
    return 0


if __name__ == '__main__':
    sys.exit(main())

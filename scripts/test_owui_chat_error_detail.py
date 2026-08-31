#!/usr/bin/env python3
"""Self-check for the chat error-detail patch (issue #1569).

Bug: chat_web_search_handler and two sibling call sites in
open_webui/utils/middleware.py read an upstream failure via
error_body.get('detail', <hardcoded default>). Hive's edge-api never sends a
top-level detail key, only an OpenAI-shaped envelope nested under an outer
error key carrying code, message and type. The lookup always missed, so
every failure surfaced as the same hardcoded string regardless of the real
cause. Cost real investigation time on issue #1567 (a 401 auth-filter gap
read as a generic query-generation failure).

RED and GREEN are both proved here, not asserted from memory: the old bare
lookup, run against a realistic Hive error body, is shown discarding the
real message (RED), then the same body through the patched shared helper is
shown returning it (GREEN).

Runs the real build-time patch against a copy of the vendored source and
asserts on the result. Structural, no framework, no network.
Run: python3 scripts/test_owui_chat_error_detail.py
"""

import ast
import os
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[1]
PATCH = REPO_ROOT / "deploy/docker/owui-patches/apply_chat_error_detail_1569_patch.py"
VENDORED = REPO_ROOT / "vendor/open-webui/backend/open_webui/utils/middleware.py"

# The shape Hive's edge-api writeAuthError actually emits
# (apps/edge-api/internal/errors/middleware.go): an outer "error" object
# carrying code, message and type. No top-level "detail" key anywhere.
REAL_MESSAGE = "Session expired, please sign in again."
HIVE_ERROR_BODY = dict(
    error=dict(code="UNAUTHENTICATED", message=REAL_MESSAGE, type="UNAUTHORIZED")
)


def run_patch(dest_dir: Path):
    dest = dest_dir / "middleware.py"
    shutil.copy(VENDORED, dest)
    env = dict(os.environ)
    env["HIVE_OWUI_MIDDLEWARE_PY"] = str(dest)
    r = subprocess.run(
        [sys.executable, str(PATCH)], env=env, capture_output=True, text=True
    )
    return r, dest


def load_helper(patched_source: str):
    """Import just the shared helper out of the patched module.

    The module pulls in the whole Open WebUI backend, which is not installed
    here, so the function is extracted and executed on its own. This runs
    the real shipped code, not a reimplementation that could agree with a
    broken original.
    """
    tree = ast.parse(patched_source)
    for node in tree.body:
        if (
            isinstance(node, ast.FunctionDef)
            and node.name == "_hive_extract_upstream_error_message"
        ):
            ns = dict()
            exec(compile(ast.Module([node], []), "<patched>", "exec"), dict(), ns)
            return ns["_hive_extract_upstream_error_message"]
    return None


def old_buggy_lookup(error_body, default):
    """The exact pre-fix line, for the RED half of this check."""
    return error_body.get("detail", default)


def main() -> int:
    checks = dict()

    # RED: prove the bug is real before proving the fix. The old lookup,
    # against Hive's actual error shape, discards the real message.
    checks["RED: old lookup misses Hive's error shape"] = (
        old_buggy_lookup(HIVE_ERROR_BODY, "Query generation failed")
        == "Query generation failed"
    )

    with tempfile.TemporaryDirectory() as tmp:
        result, dest = run_patch(Path(tmp))
        checks["patch script exits 0"] = result.returncode == 0
        if result.returncode != 0:
            print(result.stdout)
            print(result.stderr)
            print("FAIL: patch script did not run")
            return 1

        patched = dest.read_text()
        checks["patched module still parses"] = _parses(patched)

        # Idempotency: a second run must no-op, not raise or double-splice.
        again = subprocess.run(
            [sys.executable, str(PATCH)],
            env=dict(os.environ, HIVE_OWUI_MIDDLEWARE_PY=str(dest)),
            capture_output=True,
            text=True,
        )
        checks["re-running the patch is a no-op"] = (
            again.returncode == 0 and dest.read_text() == patched
        )

        checks["web-search handler no longer reads bare detail"] = (
            "detail = error_body.get('detail', 'Query generation failed')"
            not in patched
        )
        checks["image-prompt handler no longer reads bare detail"] = (
            "detail = error_body.get('detail', 'Image prompt generation failed')"
            not in patched
        )
        checks["non_streaming handler no longer reads bare detail"] = (
            "error = error.get('detail', error)" not in patched
        )

        # def plus 3 call sites, all routed through the one shared helper.
        checks["3 call sites now call the shared helper"] = (
            patched.count("_hive_extract_upstream_error_message(") == 4
        )

        helper = load_helper(patched)
        checks["patch defines the shared helper"] = helper is not None
        if helper is None:
            return _report(checks)

        # GREEN: the real message survives, through the actual shipped code.
        checks["GREEN: nested OpenAI-shaped message extracted"] = (
            helper(HIVE_ERROR_BODY, "Query generation failed") == REAL_MESSAGE
        )
        # non_streaming_chat_response_handler hands the helper the already
        # unwrapped inner error object, not the top-level body. Both shapes
        # must work, since different call sites hold different shapes.
        checks["GREEN: already-unwrapped error object also extracted"] = (
            helper(HIVE_ERROR_BODY["error"], "fallback") == REAL_MESSAGE
        )
        # Back-compat: a genuine FastAPI detail-shaped body (a route that
        # never left the vendored stack) still works.
        checks["FastAPI detail shape still extracted"] = (
            helper(dict(detail="plain fastapi detail"), "fallback")
            == "plain fastapi detail"
        )
        # No usable text anywhere: the caller's own default is returned,
        # verbatim, asserting no cause the helper does not know.
        checks["fallback default returned when nothing usable exists"] = (
            helper(dict(error=dict(code="X")), "Query generation failed")
            == "Query generation failed"
        )
        checks["fallback default returned for a non-dict payload"] = (
            helper(None, "Query generation failed") == "Query generation failed"
        )

    return _report(checks)


def _parses(text: str) -> bool:
    try:
        ast.parse(text)
        return True
    except SyntaxError:
        return False


def _report(checks) -> int:
    failed = [k for k, v in sorted(checks.items()) if not v]
    for k in sorted(checks):
        print(("PASS " if checks[k] else "FAIL ") + k)
    if failed:
        print("FAIL: " + ", ".join(failed))
        return 1
    print("OK")
    return 0


if __name__ == "__main__":
    sys.exit(main())

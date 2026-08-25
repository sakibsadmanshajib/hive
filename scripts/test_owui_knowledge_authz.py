#!/usr/bin/env python3
"""Self-check for Knowledge by-id ownership enforcement (issue #1056).

Runs the real build-time patch against a copy of the vendored knowledge.py and
asserts the result. Structural, no framework, no network.
Run: python3 scripts/test_owui_knowledge_authz.py
"""

import ast
import os
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[1]
PATCH = REPO_ROOT / "deploy/docker/owui-patches/apply_knowledge_authz_patch.py"
VENDORED = REPO_ROOT / "vendor/open-webui/backend/open_webui/routers/knowledge.py"

def main() -> int:
    vendored = VENDORED.read_text()
    if "user.role == 'admin'\n            or knowledge.user_id == user.id" not in vendored:
        print("FAIL: vendored source drifted; update this check")
        return 1
    dest = Path(tempfile.mkdtemp()) / "knowledge.py"
    shutil.copy(VENDORED, dest)
    env = dict(os.environ)
    env["HIVE_OWUI_KNOWLEDGE_PY"] = str(dest)
    r = subprocess.run([sys.executable, str(PATCH)], env=env,
                       capture_output=True, text=True)
    if r.returncode != 0:
        print("FAIL: patch failed:", r.stdout + r.stderr)
        return 1
    patched = dest.read_text()
    checks = {
        "four markers": patched.count("# hive (#1056)") == 4,
        "GET gate flag-gated":
            "(user.role == 'admin' and BYPASS_ADMIN_ACCESS_CONTROL)\n            or knowledge.user_id == user.id" in patched,
        "/files gates flag-gated x2":
            patched.count("(user.role == 'admin' and BYPASS_ADMIN_ACCESS_CONTROL)\n        or knowledge.user_id == user.id") == 2,
        "export uses get_verified_user":
            "Depends(get_verified_user), db: AsyncSession" in patched,
        "old short-circuits gone":
            "user.role == 'admin'\n            or knowledge.user_id == user.id" not in patched
            and "user.role == 'admin'\n        or knowledge.user_id == user.id" not in patched,
        "patched source compiles": isinstance(ast.parse(patched), ast.Module),
        "negative control intact":
            "user.role == 'admin'\n            or knowledge.user_id == user.id" in vendored,
    }
    failed = [k for k, v in checks.items() if not v]
    for k, v in checks.items():
        print(("PASS " if v else "FAIL ") + k)
    if failed:
        print("FAIL: " + ", ".join(failed))
        return 1
    print("ok: knowledge by-id routes enforce ownership (#1056)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

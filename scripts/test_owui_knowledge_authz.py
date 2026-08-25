#!/usr/bin/env python3
"""Self-check for the owui authz patches (#1056 knowledge reads, #1186 family).

Runs the real build-time patch scripts against copies of the vendored backend
source and asserts the result. Structural, no framework, no network.
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
PATCHES = REPO_ROOT / "deploy/docker/owui-patches"
VENDORED = REPO_ROOT / "vendor/open-webui/backend/open_webui"

# Per-file marker totals that must hold after patching. Mirrors the Dockerfile
# consolidated drift guard and EXPECTED_MARKERS inside
# apply_router_authz_family_patch.py.
FAMILY_MARKERS = {
    "knowledge.py": 12,
    "files.py": 10,
    "evaluations.py": 4,
    "folders.py": 7,
    "calendar.py": 2,
    "chats.py": 7,
    "prompts.py": 11,
    "notes.py": 6,
    "tools.py": 10,
    "models.py": 5,
}


def run_patch(patch_path: Path, env_key: str, src_path: Path, dest_dir: Path):
    dest = dest_dir / src_path.name
    shutil.copy(src_path, dest)
    env = dict(os.environ)
    env[env_key] = str(dest)
    r = subprocess.run(
        [sys.executable, str(patch_path)], env=env, capture_output=True, text=True
    )
    return r, dest


def report(checks):
    failed = []
    for k in sorted(checks):
        v = checks[k]
        print(("PASS " if v else "FAIL ") + k)
        if not v:
            failed.append(k)
    if failed:
        print("FAIL: " + ", ".join(failed))
    return 1 if failed else 0


def check_knowledge() -> int:
    """#1056 (PR #1183): knowledge by-id read routes enforce ownership."""
    vendored = VENDORED / "routers/knowledge.py"
    src = vendored.read_text()
    checks = {
        "knowledge: vendored still has unflagged read gate (negative control)": (
            "user.role == 'admin'\n            or knowledge.user_id == user.id" in src
        ),
    }
    tmp = Path(tempfile.mkdtemp())
    r, dest = run_patch(
        PATCHES / "apply_knowledge_authz_patch.py",
        "HIVE_OWUI_KNOWLEDGE_PY", vendored, tmp,
    )
    if r.returncode != 0:
        print("FAIL: knowledge patch failed:", r.stdout + r.stderr)
        return 1
    patched = dest.read_text()
    checks.update({
        "knowledge: four #1056 markers": patched.count("# hive (#1056)") == 4,
        "knowledge: GET gate flag-gated":
            "(user.role == 'admin' and BYPASS_ADMIN_ACCESS_CONTROL)\n"
            "            or knowledge.user_id == user.id" in patched,
        "knowledge: export uses get_verified_user":
            "Depends(get_verified_user), db: AsyncSession" in patched,
        "knowledge: old short-circuits gone":
            "user.role == 'admin'\n            or knowledge.user_id == user.id" not in patched
            and "user.role == 'admin'\n        or knowledge.user_id == user.id" not in patched,
        "knowledge: patched source compiles":
            isinstance(ast.parse(patched), ast.Module),
    })
    return report(checks)


def check_retrieval() -> int:
    """#1186 HIGH slice: filter_accessible_collections admin bypass."""
    utils_path = VENDORED / "retrieval/utils.py"
    src = utils_path.read_text()
    checks = {
        "retrieval: vendored still has unflagged bypass (negative control)": (
            "    if user.role == 'admin':\n        return safe_names" in src
        ),
    }
    tmp = Path(tempfile.mkdtemp())
    r, dest = run_patch(
        PATCHES / "apply_retrieval_authz_patch.py",
        "HIVE_OWUI_RETRIEVAL_UTILS_PY", utils_path, tmp,
    )
    if r.returncode != 0:
        print("FAIL: retrieval patch failed:", r.stdout + r.stderr)
        return 1
    patched = dest.read_text()
    checks.update({
        "retrieval: two markers": patched.count("# hive (#1186)") == 2,
        "retrieval: choke point flag-gated":
            "user.role == 'admin' and BYPASS_ADMIN_ACCESS_CONTROL:\n"
            "        return safe_names" in patched,
        "retrieval: old bypass gone":
            "    if user.role == 'admin':\n        return safe_names" not in patched,
        "retrieval: patched source compiles":
            isinstance(ast.parse(patched), ast.Module),
    })
    return report(checks)



def main() -> int:
    rc = check_knowledge() + check_retrieval()
    if rc:
        print("FAIL: owui authz patch self-check failed")
        return 1
    print("ok: knowledge by-id ownership (#1056) and retrieval filter")
    print("ok: flag-gating (#1186 HIGH slice) enforced")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

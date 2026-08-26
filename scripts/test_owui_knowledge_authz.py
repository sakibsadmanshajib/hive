#!/usr/bin/env python3
"""Self-check for the owui authz patches (#1056 knowledge reads, #1186 family,
#1191 residuals).

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


def check_router_family() -> int:
    """#1186 family sweep: every router gate rewritten to the #960 predicate."""
    routers = VENDORED / "routers"
    patch = PATCHES / "apply_router_authz_family_patch.py"
    # Negative controls: each file must still contain an unflagged pattern
    # pre-patch, otherwise this test can no longer go red on vendor drift.
    unflagged = {
        "knowledge.py": "and user.role != 'admin'",
        "files.py": "or user.role == 'admin' or await has_access_to_file(",
        "evaluations.py": "if user.role == 'admin':\n        feedback = await Feedbacks.",
        "folders.py": "is_admin = user.role == 'admin'",
        "calendar.py": "if cal.user_id == user.id or user.role == 'admin':",
        "chats.py": "if chat.user_id != user.id and user.role != 'admin':",
        "prompts.py": "and user.role != 'admin'\n    ):",
        "notes.py": "if user.role != 'admin' and (\n        user.id != note.user_id",
        "tools.py": "and user.role != 'admin'\n    ):",
        "models.py": "if not knowledge_items or user.role == 'admin':",
    }
    checks = {}
    tmp = Path(tempfile.mkdtemp())
    for name, frag in unflagged.items():
        src_text = (routers / name).read_text()
        checks[f"{name}: vendored still has unflagged pattern (negative control)"] = (
            frag in src_text
        )
        shutil.copy(routers / name, tmp / name)
    env = dict(os.environ)
    env["HIVE_OWUI_ROUTERS_DIR"] = str(tmp)
    r = subprocess.run(
        [sys.executable, str(patch)], env=env, capture_output=True, text=True
    )
    if r.returncode != 0:
        print("FAIL: router family patch failed:", r.stdout + r.stderr)
        return 1
    for name, expected in FAMILY_MARKERS.items():
        text = (tmp / name).read_text()
        checks[f"{name}: {expected} markers after patch"] = (
            text.count("# hive (#1186)") == expected
        )
        checks[f"{name}: patched source compiles"] = isinstance(
            ast.parse(text), ast.Module
        )
    return report(checks)

def check_authz_residuals() -> int:
    """#1191: shared-chat grant-skip and global reindex are flag-gated."""
    routers = VENDORED / "routers"
    patch = PATCHES / "apply_authz_residuals_1191_patch.py"
    CHATS_OLD = (
        "    if shared and user.role != 'admin' and shared.user_id != user.id:\n"
    )
    KNOW_OLD = (
        "    if user.role != 'admin':\n"
        "        raise HTTPException(\n"
        "            status_code=status.HTTP_401_UNAUTHORIZED,\n"
        "            detail=ERROR_MESSAGES.UNAUTHORIZED,\n"
        "        )\n"
    )
    checks = {
        "chats: vendored still has unflagged grant skip (negative control)":
            CHATS_OLD in (routers / "chats.py").read_text(),
        "knowledge: vendored still has bare reindex guard (negative control)":
            KNOW_OLD in (routers / "knowledge.py").read_text(),
    }
    tmp = Path(tempfile.mkdtemp())
    shutil.copy(routers / "chats.py", tmp / "chats.py")
    shutil.copy(routers / "knowledge.py", tmp / "knowledge.py")
    env = dict(os.environ)
    env["HIVE_OWUI_ROUTERS_DIR"] = str(tmp)
    r = subprocess.run(
        [sys.executable, str(patch)], env=env, capture_output=True, text=True
    )
    if r.returncode != 0:
        print("FAIL: authz residuals patch failed:", r.stdout + r.stderr)
        return 1
    for name in ("chats.py", "knowledge.py"):
        text = (tmp / name).read_text()
        checks[f"{name}: one #1191 marker after patch"] = (
            text.count("# hive (#1191)") == 1
        )
        checks[f"{name}: patched source compiles"] = isinstance(
            ast.parse(text), ast.Module
        )
    checks["chats: old grant skip gone"] = CHATS_OLD not in (tmp / "chats.py").read_text()
    checks["chats: flag-or-grant predicate present"] = (
        "and not (user.role == 'admin' and ENABLE_ADMIN_CHAT_ACCESS)" in (tmp / "chats.py").read_text()
    )
    checks["knowledge: bare reindex guard gone"] = KNOW_OLD not in (tmp / "knowledge.py").read_text()
    checks["knowledge: BYPASS predicate present"] = (
        "if not (user.role == 'admin' and BYPASS_ADMIN_ACCESS_CONTROL):" in (tmp / "knowledge.py").read_text()
    )
    return report(checks)


def main() -> int:
    rc = check_knowledge()
    rc += check_retrieval()
    rc += check_router_family()
    rc += check_authz_residuals()
    if rc:
        print("FAIL: owui authz patch self-check failed")
        return 1
    print("ok: knowledge by-id ownership (#1056), retrieval filter and")
    print("ok: router family flag-gating (#1186) and residuals (#1191) all enforced")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

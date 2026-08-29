#!/usr/bin/env python3
"""Self-check for the skill group-grant filter patch (issue #1396, skills half).

A skill's body is appended verbatim to the chat request as a system message,
so a skill is an instruction that lands in somebody's prompt. Open WebUI's
shared grant filter never inspects group grants, so a hand-built create or
access-update carrying another tenant's group id is stored unquestioned on
this shared instance.

This runs the real build-time patch script against a copy of the vendored
router, then exercises the inserted helper's logic directly against fake
principals. Each check carries its negative control: the unpatched vendored
source is asserted to still have the hole, so the test goes red on vendor
drift instead of quietly passing over a file that no longer matches.

Run: python3 scripts/test_owui_skill_group_grants.py
"""
import ast
import asyncio
import os
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[1]
VENDORED_ROUTERS = REPO_ROOT / "vendor/open-webui/backend/open_webui/routers"
PATCH = REPO_ROOT / "deploy/docker/owui-patches/apply_skill_group_grants_patch.py"

MARKER = "# hive (#1396)"
CALL_SITES = 3


class FakeGroup:
    def __init__(self, group_id):
        self.id = group_id


class FakeGroups:
    """Stands in for open_webui.models.groups.Groups, which only ever returns
    the groups the caller is actually a member of."""

    def __init__(self, memberships):
        self.memberships = memberships

    async def get_groups_by_member_id(self, user_id, db=None):
        return [FakeGroup(g) for g in self.memberships.get(user_id, [])]


class FakeUser:
    def __init__(self, user_id, role="user"):
        self.id = user_id
        self.role = role


def patched_router_source(tmp: Path) -> str:
    shutil.copy(VENDORED_ROUTERS / "skills.py", tmp / "skills.py")
    env = dict(os.environ)
    env["HIVE_OWUI_ROUTERS_DIR"] = str(tmp)
    subprocess.run([sys.executable, str(PATCH)], check=True, env=env, capture_output=True)
    return (tmp / "skills.py").read_text()


def load_helper(source: str):
    """Extract the inserted helper and execute it in isolation.

    Exec'ing the whole router would need FastAPI, the database and the rest of
    Open WebUI. The helper closes over exactly one name, Groups, so lifting the
    function definition out and giving it a fake is enough to exercise the
    filtering decision, which is the thing under test.
    """
    tree = ast.parse(source)
    fn = next(
        node
        for node in tree.body
        if isinstance(node, ast.AsyncFunctionDef) and node.name == "_hive_filter_group_grants"
    )
    namespace = {}
    exec(compile(ast.Module(body=[fn], type_ignores=[]), "<helper>", "exec"), namespace)
    return namespace["_hive_filter_group_grants"]


def run(helper, groups, user, grants):
    original_groups = helper.__globals__.get("Groups")
    helper.__globals__["Groups"] = groups
    try:
        return asyncio.run(helper(user, grants, None))
    finally:
        if original_groups is None:
            helper.__globals__.pop("Groups", None)
        else:
            helper.__globals__["Groups"] = original_groups


def main() -> None:
    failures = []

    def check(name, condition):
        if not condition:
            failures.append(name)

    vendored = (VENDORED_ROUTERS / "skills.py").read_text()

    # Negative controls. If either of these stops holding, the vendored source
    # moved and every assertion below is measuring the wrong file.
    check(
        "vendored skills.py still carries the unfiltered grant assignment (negative control)",
        vendored.count("'sharing.public_skills',\n    )\n") == CALL_SITES,
    )
    check(
        "vendored skills.py is unpatched before the build runs (negative control)",
        MARKER not in vendored,
    )

    tmp = Path(tempfile.mkdtemp())
    try:
        patched = patched_router_source(tmp)
    finally:
        pass

    check(
        f"the patch marks the helper plus all {CALL_SITES} write sites",
        patched.count(MARKER) == CALL_SITES + 1,
    )
    check(
        "the patched router is still valid Python",
        _parses(patched),
    )
    check(
        "every filter_allowed_access_grants call is followed by the group filter",
        patched.count("_hive_filter_group_grants(") == CALL_SITES + 1,
    )

    helper = load_helper(patched)
    groups = FakeGroups({"member": ["tenant-a"], "attacker": ["tenant-b"]})

    foreign = [{"principal_type": "group", "principal_id": "tenant-a", "permission": "read"}]
    own = [{"principal_type": "group", "principal_id": "tenant-b", "permission": "read"}]

    check(
        "a member cannot grant a skill to a group they are not in",
        run(helper, groups, FakeUser("attacker"), list(foreign)) == [],
    )
    check(
        "a member keeps a grant to their own group",
        run(helper, groups, FakeUser("attacker"), list(own)) == own,
    )
    check(
        "an admin is untouched, because only a platform admin holds that role here",
        run(helper, groups, FakeUser("attacker", role="admin"), list(foreign)) == foreign,
    )

    mixed = [
        {"principal_type": "user", "principal_id": "someone", "permission": "read"},
        {"principal_type": "group", "principal_id": "tenant-a", "permission": "read"},
        {"principal_type": "group", "principal_id": "tenant-b", "permission": "write"},
    ]
    check(
        "only the foreign group grant is dropped, user grants and own-group grants survive",
        run(helper, groups, FakeUser("attacker"), list(mixed)) == [mixed[0], mixed[2]],
    )

    check(
        "an empty grant list is returned as-is without a database round trip",
        run(helper, None, FakeUser("attacker"), []) == [],
    )

    # Object-shaped grants, which is what the Pydantic form actually carries;
    # dict-shaped is only what a raw payload looks like before validation.
    class ObjectGrant:
        def __init__(self, principal_type, principal_id):
            self.principal_type = principal_type
            self.principal_id = principal_id

    object_grants = [ObjectGrant("group", "tenant-a"), ObjectGrant("group", "tenant-b")]
    kept = run(helper, groups, FakeUser("attacker"), list(object_grants))
    check(
        "the filter reads attributes too, not only dict keys",
        [g.principal_id for g in kept] == ["tenant-b"],
    )

    shutil.rmtree(tmp, ignore_errors=True)

    for failure in failures:
        print(f"FAIL: {failure}")
    if failures:
        sys.exit(1)
    print("ok: owui skill group-grant filter (issue #1396)")


def _parses(source: str) -> bool:
    try:
        ast.parse(source)
    except SyntaxError:
        return False
    return True


if __name__ == "__main__":
    main()

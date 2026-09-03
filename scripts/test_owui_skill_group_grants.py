#!/usr/bin/env python3
"""Self-check for the group-grant filter patch (issue #1396, the two routers
whose payload reaches a model prompt).

A skill's body is appended verbatim to the chat request as a system message and
a knowledge collection's passages arrive as retrieved context, so both are text
that lands in somebody's prompt. Open WebUI's shared grant filter never
inspects group grants, so a hand-built create or access-update carrying another
tenant's group id is stored unquestioned on this shared instance.

Knowledge joined the patch in #1505, which is the change that first lets a
non-admin own a collection and therefore the change that first makes granting
one reachable.

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
# Router -> the number of filter_allowed_access_grants call sites in it, which
# is also the marker count minus the one on the inserted helper. Mirrors
# TARGETS in the patch script; asserted against the vendored source below, so a
# vendor bump that adds or removes a site fails here rather than half-applying.
CALL_SITES = {"skills.py": 3, "knowledge.py": 6}
PUBLIC_KEYS = {"skills.py": "sharing.public_skills", "knowledge.py": "sharing.public_knowledge"}


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


def patched_router_sources(tmp: Path) -> dict:
    """Run the real build-time script once, over copies of every target."""
    for name in CALL_SITES:
        shutil.copy(VENDORED_ROUTERS / name, tmp / name)
    env = dict(os.environ)
    env["HIVE_OWUI_ROUTERS_DIR"] = str(tmp)
    subprocess.run([sys.executable, str(PATCH)], check=True, env=env, capture_output=True)
    return {name: (tmp / name).read_text() for name in CALL_SITES}


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

    # Negative controls, per router. If any of these stops holding, the
    # vendored source moved and every assertion below is measuring the wrong
    # file.
    for name, sites in CALL_SITES.items():
        vendored = (VENDORED_ROUTERS / name).read_text()
        check(
            f"vendored {name} still carries {sites} unfiltered grant assignments (negative control)",
            vendored.count(f"'{PUBLIC_KEYS[name]}',\n    )\n") == sites,
        )
        check(
            f"vendored {name} is unpatched before the build runs (negative control)",
            MARKER not in vendored,
        )

    tmp = Path(tempfile.mkdtemp())
    patched_by_router = patched_router_sources(tmp)

    for name, sites in CALL_SITES.items():
        patched = patched_by_router[name]
        check(
            f"the patch marks the helper plus all {sites} write sites in {name}",
            patched.count(MARKER) == sites + 1,
        )
        check(
            f"the patched {name} is still valid Python",
            _parses(patched),
        )
        check(
            f"every filter_allowed_access_grants call in {name} is followed by the group filter",
            patched.count("_hive_filter_group_grants(") == sites + 1,
        )

    # EVERY NAME THE INSERTED CALL USES MUST EXIST WHERE IT IS INSERTED.
    #
    # This is not a hypothetical. The first version of the knowledge half
    # passed `db` to the helper, copied from the skills call sites where the
    # route declares that dependency. knowledge.py's create deliberately does
    # not (it says so in a comment: it must not hold a connection across an
    # embedding call), so the inserted line raised NameError and answered 500
    # to the very request this whole change exists to make work. Nothing
    # caught it: the patch applied cleanly, the module parsed, the marker
    # counts matched and every assertion here passed. Only a live capture did.
    #
    # An anchor-based patch cannot know which locals a route has, so the check
    # is structural: for every function containing an inserted call, every bare
    # name that call passes has to be a parameter of that function or something
    # assigned in it before the call.
    for name, source in patched_by_router.items():
        for fn in ast.walk(ast.parse(source)):
            if not isinstance(fn, (ast.AsyncFunctionDef, ast.FunctionDef)):
                continue
            calls = [
                node
                for node in ast.walk(fn)
                if isinstance(node, ast.Call)
                and isinstance(node.func, ast.Name)
                and node.func.id == "_hive_filter_group_grants"
            ]
            if not calls:
                continue
            bound = {a.arg for a in fn.args.args} | {a.arg for a in fn.args.kwonlyargs}
            for node in ast.walk(fn):
                if isinstance(node, ast.Name) and isinstance(node.ctx, ast.Store):
                    bound.add(node.id)
            for call in calls:
                passed = [a.id for a in call.args if isinstance(a, ast.Name)]
                passed += [
                    kw.value.id for kw in call.keywords if isinstance(kw.value, ast.Name)
                ]
                unbound = [n for n in passed if n not in bound]
                check(
                    f"{name}: the call in {fn.name} passes only names that exist there "
                    f"(unbound: {unbound})",
                    not unbound,
                )

    # The helper is byte-identical in every target, so its behaviour is
    # exercised once, from the router that has the most call sites.
    helper = load_helper(patched_by_router["knowledge.py"])
    groups = FakeGroups({"member": ["tenant-a"], "attacker": ["tenant-b"]})

    foreign = [{"principal_type": "group", "principal_id": "tenant-a", "permission": "read"}]
    own = [{"principal_type": "group", "principal_id": "tenant-b", "permission": "read"}]

    check(
        "a member cannot grant a resource to a group they are not in",
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
    print(
        "ok: owui group-grant filter over "
        + ", ".join(sorted(CALL_SITES))
        + " (issues #1396, #1505)"
    )


def _parses(source: str) -> bool:
    try:
        ast.parse(source)
    except SyntaxError:
        return False
    return True


if __name__ == "__main__":
    main()

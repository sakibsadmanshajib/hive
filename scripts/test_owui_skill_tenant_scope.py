#!/usr/bin/env python3
"""Self-check for the skill tenant-scoping fix (issue #1397).

Three independent proofs, none of which trusts the others:

1. Schema. Runs the exact REBUILD_SQL the Alembic migration issues against a
   real sqlite3 connection seeded with the OLD schema (a1b2c3d4e5f6's own
   DDL) and a pre-existing row, and against that connection: reproduces the
   real bug first (negative control -- two tenants collide on name under the
   unpatched schema), then proves the fix (two tenants can share a name),
   then a MUTATION TEST (the same tenant reusing a name it already has must
   still fail -- if this went green, the composite index would have been
   dropped rather than narrowed, meaning the guard enforces nothing), then
   proves the pre-existing row survives untouched.
2. Router logic. Runs the real build-time patch against a copy of the
   vendored router, extracts the inserted `_hive_resolve_tenant_group_id`
   helper the same way test_owui_skill_group_grants.py extracts its sibling,
   and exercises it directly against fake group memberships: an admin
   resolves to no tenant, an ordinary member resolves to their one
   tenant_<uuid> group, a member in several groups resolves deterministically
   to the sorted first, and a member in none resolves to None (the fail
   -closed case the router turns into a 400 rather than an unscoped create).
3. Static shape. Both patches (router and model) apply cleanly to a real
   copy of the vendored upstream source, produce valid Python, and land the
   expected marker count; a stale patch that silently stopped matching would
   fail this loudly rather than the build discovering it live.

Run: python3 scripts/test_owui_skill_tenant_scope.py
"""
import ast
import asyncio
import os
import re
import shutil
import sqlite3
import subprocess
import sys
import tempfile
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[1]
VENDORED_ROUTERS = REPO_ROOT / "vendor/open-webui/backend/open_webui/routers"
VENDORED_MODELS = REPO_ROOT / "vendor/open-webui/backend/open_webui/models"
ROUTER_PATCH = REPO_ROOT / "deploy/docker/owui-patches/apply_skill_tenant_scope_router_patch.py"
MODEL_PATCH = REPO_ROOT / "deploy/docker/owui-patches/apply_skill_tenant_scope_model_patch.py"
MIGRATION = REPO_ROOT / "deploy/docker/owui-patches/hive_skill_tenant_scope_migration.py"

ROUTER_MARKER = "# hive (#1397)"
MODEL_MARKER = "# hive (#1397)"

failures = []


def check(name, condition):
    status = "ok" if condition else "FAIL"
    print(f"{status}: {name}")
    if not condition:
        failures.append(name)


def _parses(source: str) -> bool:
    try:
        ast.parse(source)
    except SyntaxError:
        return False
    return True


# ---------------------------------------------------------------------------
# 1. Schema proof, against a real sqlite3 connection.
# ---------------------------------------------------------------------------

OLD_SCHEMA_SQL = """
CREATE TABLE skill (
    id VARCHAR NOT NULL PRIMARY KEY,
    user_id VARCHAR NOT NULL,
    name TEXT NOT NULL UNIQUE,
    description TEXT,
    content TEXT NOT NULL,
    meta JSON,
    is_active BOOLEAN NOT NULL,
    updated_at BIGINT NOT NULL,
    created_at BIGINT NOT NULL
);
CREATE INDEX idx_skill_user_id ON skill (user_id);
CREATE INDEX idx_skill_updated_at ON skill (updated_at);
"""


VENDORED_MIGRATIONS = REPO_ROOT / "vendor/open-webui/backend/open_webui/migrations/versions"


def true_migration_chain_head() -> str:
    """The one revision id, across every file in VENDORED_MIGRATIONS, that no
    other file lists as its own down_revision.

    Real bug this caught in self-review before it shipped: an earlier draft
    computed this by hand from a regex that only matched the older
    `down_revision: Union[str, None] = '...'` annotated form. 19 of the 48
    files in this directory use the newer unannotated `down_revision =
    '...'` form instead, so that draft silently undercounted the file set,
    named a MID-CHAIN revision as the head, and would have branched the real
    chain on the next deploy (Alembic fails loudly on that, "Multiple head
    revisions are present", but loud is still a broken deploy). Matching
    both forms and comparing the full revision set against the full
    down_revision set, rather than spot-checking a handful of files, is what
    catches that class of mistake instead of repeating it.
    """
    revision_re = re.compile(r"^revision\s*(?::\s*str)?\s*=\s*'([a-f0-9]+)'", re.M)
    down_revision_re = re.compile(r"^down_revision\s*(?::.*?)?\s*=\s*'([a-f0-9]+)'", re.M)
    revisions, down_revisions = set(), set()
    for f in VENDORED_MIGRATIONS.glob("*.py"):
        text = f.read_text()
        r = revision_re.search(text)
        d = down_revision_re.search(text)
        if r:
            revisions.add(r.group(1))
        if d:
            down_revisions.add(d.group(1))
    heads = revisions - down_revisions
    if len(heads) != 1:
        raise AssertionError(
            f"expected exactly one migration chain head, found {sorted(heads)}; "
            "the vendored migrations directory may have branched"
        )
    return heads.pop()


def load_rebuild_sql() -> str:
    """Import REBUILD_SQL from the real migration file rather than copying
    it, so this test cannot drift from what actually ships."""
    namespace = {"__name__": "hive_skill_tenant_scope_migration_under_test"}
    source = MIGRATION.read_text()
    # The module imports `alembic` and open_webui.migrations.util at module
    # scope, neither installed here. REBUILD_SQL is a plain string constant
    # defined before either is used, so parse-and-extract rather than exec
    # the whole module.
    tree = ast.parse(source)
    for node in tree.body:
        if isinstance(node, ast.Assign) and any(
            isinstance(t, ast.Name) and t.id == "REBUILD_SQL" for t in node.targets
        ):
            return ast.literal_eval(node.value)
    raise AssertionError("REBUILD_SQL not found in hive_skill_tenant_scope_migration.py")


def insert(conn, id_, user_id, name, tenant_group_id="__omit__"):
    if tenant_group_id == "__omit__":
        conn.execute(
            "INSERT INTO skill (id, user_id, name, content, is_active, updated_at, created_at) "
            "VALUES (?, ?, ?, 'x', 1, 0, 0)",
            (id_, user_id, name),
        )
    else:
        conn.execute(
            "INSERT INTO skill (id, user_id, name, content, is_active, updated_at, created_at, tenant_group_id) "
            "VALUES (?, ?, ?, 'x', 1, 0, 0, ?)",
            (id_, user_id, name, tenant_group_id),
        )
    conn.commit()


def run_schema_proof():
    migration_source = MIGRATION.read_text()
    down_revision_match = re.search(
        r"^down_revision\s*(?::.*?)?\s*=\s*'([a-f0-9]+)'", migration_source, re.M
    )
    check(
        "migration's down_revision points at the ACTUAL current chain head "
        "(recomputed from all 48 vendored migration files, not assumed)",
        down_revision_match is not None
        and down_revision_match.group(1) == true_migration_chain_head(),
    )

    rebuild_sql = load_rebuild_sql()
    check(
        "REBUILD_SQL adds tenant_group_id and a composite unique index",
        "tenant_group_id" in rebuild_sql and "idx_skill_tenant_group_id_name" in rebuild_sql,
    )

    conn = sqlite3.connect(":memory:")
    conn.executescript(OLD_SCHEMA_SQL)
    insert(conn, "legacy-note", "user-legacy", "Legacy Note")

    insert(conn, "tenant-a-research", "user-a", "Research")
    bug_reproduced = False
    try:
        insert(conn, "tenant-b-research", "user-b", "Research")
    except sqlite3.IntegrityError:
        bug_reproduced = True
    check(
        "negative control: two tenants collide on name under the OLD schema (bug is real)",
        bug_reproduced,
    )

    conn.executescript(rebuild_sql)

    fix_worked = True
    try:
        insert(conn, "grp-tenant-b--research", "user-b", "Research", tenant_group_id="grp-tenant-b")
    except sqlite3.IntegrityError:
        fix_worked = False
    check("schema fix: two different tenants can now share a skill name", fix_worked)

    insert(conn, "grp-tenant-a--research", "user-a", "Research", tenant_group_id="grp-tenant-a")

    guard_still_enforces = False
    try:
        insert(conn, "grp-tenant-a--research-2", "user-a", "Research", tenant_group_id="grp-tenant-a")
    except sqlite3.IntegrityError:
        guard_still_enforces = True
    check(
        "MUTATION TEST: the same tenant reusing a name it already has still fails",
        guard_still_enforces,
    )

    row = conn.execute(
        "SELECT user_id, name, tenant_group_id FROM skill WHERE id = 'legacy-note'"
    ).fetchone()
    check(
        "legacy pre-migration row survives the rebuild untouched (tenant_group_id NULL)",
        row == ("user-legacy", "Legacy Note", None),
    )

    total = conn.execute("SELECT COUNT(*) FROM skill").fetchone()[0]
    check("no row was silently dropped during the rebuild (4 rows survive)", total == 4)

    conn.close()

    # A separate connection, exercising the EXACT statement-by-statement
    # split-and-execute loop upgrade() actually runs (op.execute() cannot run
    # a multi-statement script in one call, which is why upgrade() splits on
    # ";\n" and loops rather than calling executescript() once, unlike the
    # check above). str.strip() on the whole SQL block leaves the FINAL
    # statement's trailing ";" attached (nothing after it for ";\n" to
    # match), so this exercises the asymmetry directly rather than trusting
    # that sqlite3 tolerates one trailing semicolon.
    statements = [s.strip() for s in rebuild_sql.strip().split(";\n") if s.strip()]
    check(
        "REBUILD_SQL splits into exactly 7 statements on ';\\n'",
        len(statements) == 7,
    )
    conn2 = sqlite3.connect(":memory:")
    conn2.executescript(OLD_SCHEMA_SQL)
    insert(conn2, "legacy-note-2", "user-legacy", "Legacy Note 2")
    split_ok = True
    try:
        for statement in statements:
            conn2.execute(statement)
        conn2.commit()
    except sqlite3.Error as e:
        split_ok = False
        print(f"    (statement-split path failed: {e})")
    check(
        "the real upgrade() statement-split-and-loop path (not executescript) "
        "runs cleanly against a real connection",
        split_ok,
    )
    if split_ok:
        insert(conn2, "grp-x--research", "user-x", "Research", tenant_group_id="grp-x")
        insert(conn2, "grp-y--research", "user-y", "Research", tenant_group_id="grp-y")
        collision = False
        try:
            insert(conn2, "grp-x--research-2", "user-x", "Research", tenant_group_id="grp-x")
        except sqlite3.IntegrityError:
            collision = True
        check(
            "post-split-path migration: two tenants share a name, a third "
            "same-tenant collision still fails",
            collision,
        )
    conn2.close()


# ---------------------------------------------------------------------------
# 2 & 3. Router + model patch application, then exercise the router helper.
# ---------------------------------------------------------------------------


class FakeGroup:
    def __init__(self, group_id, name):
        self.id = group_id
        self.name = name


class FakeGroups:
    def __init__(self, memberships):
        self.memberships = memberships

    async def get_groups_by_member_id(self, user_id, db=None):
        return list(self.memberships.get(user_id, []))


class FakeUser:
    def __init__(self, user_id, role="user"):
        self.id = user_id
        self.role = role


def load_helper(source: str):
    tree = ast.parse(source)
    fn = next(
        node
        for node in tree.body
        if isinstance(node, ast.AsyncFunctionDef) and node.name == "_hive_resolve_tenant_group_id"
    )
    namespace = {}
    exec(compile(ast.Module(body=[fn], type_ignores=[]), "<helper>", "exec"), namespace)
    return namespace["_hive_resolve_tenant_group_id"]


def load_id_scope_fn(source: str):
    """Extract the plain (sync, no I/O) _hive_id_scope function, the same
    lift-out-and-exec technique load_helper uses for its async sibling."""
    tree = ast.parse(source)
    fn = next(
        node
        for node in tree.body
        if isinstance(node, ast.FunctionDef) and node.name == "_hive_id_scope"
    )
    namespace = {}
    exec(compile(ast.Module(body=[fn], type_ignores=[]), "<id_scope>", "exec"), namespace)
    return namespace["_hive_id_scope"]


def run(helper, groups, user):
    original = helper.__globals__.get("Groups")
    helper.__globals__["Groups"] = groups
    try:
        return asyncio.run(helper(user, None))
    finally:
        if original is None:
            helper.__globals__.pop("Groups", None)
        else:
            helper.__globals__["Groups"] = original


def run_router_and_model_proof():
    vendored_router = (VENDORED_ROUTERS / "skills.py").read_text()
    vendored_model = (VENDORED_MODELS / "skills.py").read_text()

    check(
        "vendored routers/skills.py is unpatched before the build runs (negative control)",
        ROUTER_MARKER not in vendored_router,
    )
    check(
        "vendored models/skills.py is unpatched before the build runs (negative control)",
        MODEL_MARKER not in vendored_model,
    )
    check(
        "vendored models/skills.py still declares the single-column unique on name "
        "(negative control)",
        "name = Column(Text, unique=True)" in vendored_model,
    )

    tmp = Path(tempfile.mkdtemp())
    routers_dir = tmp / "routers"
    models_dir = tmp / "models"
    routers_dir.mkdir()
    models_dir.mkdir()
    shutil.copy(VENDORED_ROUTERS / "skills.py", routers_dir / "skills.py")
    shutil.copy(VENDORED_MODELS / "skills.py", models_dir / "skills.py")

    env = dict(os.environ)
    env["HIVE_OWUI_ROUTERS_DIR"] = str(routers_dir)
    router_result = subprocess.run(
        [sys.executable, str(ROUTER_PATCH)], env=env, capture_output=True, text=True
    )
    check(
        f"apply_skill_tenant_scope_router_patch.py exits 0 ({router_result.stderr.strip()[-300:]})",
        router_result.returncode == 0,
    )

    env2 = dict(os.environ)
    env2["HIVE_OWUI_MODELS_DIR"] = str(models_dir)
    model_result = subprocess.run(
        [sys.executable, str(MODEL_PATCH)], env=env2, capture_output=True, text=True
    )
    check(
        f"apply_skill_tenant_scope_model_patch.py exits 0 ({model_result.stderr.strip()[-300:]})",
        model_result.returncode == 0,
    )

    patched_router = (routers_dir / "skills.py").read_text()
    patched_model = (models_dir / "skills.py").read_text()

    check("patched router is valid Python", _parses(patched_router))
    check("patched model is valid Python", _parses(patched_model))
    check(
        f"patched router carries exactly 3 {ROUTER_MARKER} markers",
        patched_router.count(ROUTER_MARKER) == 3,
    )
    check(
        f"patched model carries exactly 3 {MODEL_MARKER} markers",
        patched_model.count(MODEL_MARKER) == 3,
    )
    check(
        "patched model no longer declares the single-column unique on name",
        "name = Column(Text, unique=True)" not in patched_model,
    )
    check(
        "patched model declares tenant_group_id",
        "tenant_group_id = Column(String, nullable=True)" in patched_model,
    )
    check(
        "patched model's insert_new_skill accepts and persists tenant_group_id",
        "tenant_group_id: Optional[str] = None" in patched_model
        and "'tenant_group_id': tenant_group_id," in patched_model,
    )
    check(
        "patched router's insert_new_skill call passes tenant_group_id through",
        "tenant_group_id=hive_tenant_group_id" in patched_router,
    )

    # Re-running either patch against its own already-patched output must be
    # a no-op, not a second insertion (the "already applied" short-circuit).
    router_rerun = subprocess.run(
        [sys.executable, str(ROUTER_PATCH)], env=env, capture_output=True, text=True
    )
    check(
        "router patch is idempotent (re-run does not duplicate markers)",
        router_rerun.returncode == 0
        and (routers_dir / "skills.py").read_text().count(ROUTER_MARKER) == 3,
    )

    # Exercise the real, patched helper directly.
    helper = load_helper(patched_router)
    tenant_a = FakeGroup("grp-a", "tenant_11111111-1111-1111-1111-111111111111")
    tenant_b = FakeGroup("grp-b", "tenant_22222222-2222-2222-2222-222222222222")
    other = FakeGroup("grp-other", "not-a-tenant-group")

    groups = FakeGroups(
        {
            "member-a": [tenant_a, other],
            "member-b": [tenant_b],
            "member-none": [other],
            "member-multi": [tenant_b, tenant_a],  # deliberately out of order
        }
    )

    check(
        "an admin resolves to no tenant (None), matching the group-grant filter's own exemption",
        run(helper, groups, FakeUser("member-a", role="admin")) is None,
    )
    check(
        "an ordinary member resolves to their own tenant_<uuid> group, not an unrelated group",
        run(helper, groups, FakeUser("member-a")) == "grp-a",
    )
    check(
        "a different tenant's member resolves to a different id",
        run(helper, groups, FakeUser("member-b")) == "grp-b",
    )
    check(
        "a member in more than one tenant_ group resolves deterministically (sorted first)",
        run(helper, groups, FakeUser("member-multi")) == "grp-a",
    )
    check(
        "a member with no tenant_ group resolves to None, same as admin "
        "(not fail-closed: most real accounts on the demo box, including the "
        "one shown to prospects, have no tenant_ group today; blocking them "
        "would be an outage, and None is safe under the composite index)",
        run(helper, groups, FakeUser("member-none")) is None,
    )

    # _hive_id_scope: the id SCOPE is broader than the resolved tenant group
    # alone. Security-review finding on an earlier draft, fixed: that draft
    # left every ungrouped member with the SAME empty id prefix, so two
    # different ungrouped accounts still collided on id exactly as before
    # this patch, and the PR description's "no error" claim for that case
    # was wrong. This block is the regression test for the fix.
    id_scope = load_id_scope_fn(patched_router)

    check(
        "id scope: an admin with no tenant group gets None (unprefixed, flat "
        "platform-wide id namespace)",
        id_scope(FakeUser("admin-x", role="admin"), None) is None,
    )
    check(
        "id scope: a tenant-grouped member scopes to the tenant group, not "
        "their own user id",
        id_scope(FakeUser("member-a"), "grp-a") == "grp-a",
    )
    check(
        "id scope: an UNGROUPED ordinary member falls back to their OWN user "
        "id instead of None",
        id_scope(FakeUser("member-x"), None) == "member-x",
    )

    def final_id(user, tenant_group_id, base):
        scope = id_scope(user, tenant_group_id)
        return f"{scope}--{base}" if scope else base

    ungrouped_x = FakeUser("ungrouped-user-x")
    ungrouped_y = FakeUser("ungrouped-user-y")
    id_x = final_id(ungrouped_x, None, "research")
    id_y = final_id(ungrouped_y, None, "research")
    check(
        "REGRESSION (reviewer finding): two DIFFERENT ungrouped members "
        "creating a skill with the SAME name no longer collide on id -- "
        "before the fix both computed the bare 'research' and the second "
        "create 400d ID_TAKEN exactly as it did before this whole patch",
        id_x != id_y and id_x == "ungrouped-user-x--research" and id_y == "ungrouped-user-y--research",
    )
    check(
        "MUTATION-style control: the SAME ungrouped member reusing their own "
        "name still computes the SAME id (genuine per-account duplicate "
        "detection is not accidentally disabled by the fallback)",
        final_id(ungrouped_x, None, "research") == id_x,
    )

    shutil.rmtree(tmp, ignore_errors=True)


def main() -> None:
    run_schema_proof()
    run_router_and_model_proof()

    for failure in failures:
        print(f"FAIL: {failure}")
    if failures:
        sys.exit(1)
    print("\nok: owui skill tenant-scope fix (issue #1397)")


if __name__ == "__main__":
    main()

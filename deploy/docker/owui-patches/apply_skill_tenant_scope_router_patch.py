"""Build-time router change for tenant-scoped skill uniqueness (issue #1397).

Router half of the #1397 fix. `id` is the primary key and is derived by
slugifying `name` (`form_data.id.lower().replace(' ', '-')`, unchanged by
this patch); it collides across tenants before name uniqueness is ever
reached, since the id check runs first in CreateNewSkill. This patch
resolves an id SCOPE and prefixes the id with it, so an honest slugify from
two different tenants' identical display names no longer collides on the
primary key, while a genuine duplicate within the SAME scope still 400s
ID_TAKEN, which is correct: that is real duplicate detection, not a
cross-tenant leak.

The id scope is NOT always the tenant group. Three cases, in priority order:

1. A tenant-grouped ordinary member scopes to their tenant_<uuid> OWUI
   group, shared with every other member of that tenant.
2. An UNGROUPED ordinary member (the common case on this deployment today,
   see below) scopes to their OWN user id instead of getting no scope at
   all. Reviewed and corrected after a security-review finding on an
   earlier draft of this patch: that draft gave every ungrouped member the
   SAME empty prefix, so two different ungrouped accounts still collided on
   the bare id exactly as before the fix, and the PR description claiming
   "no error" for that case was wrong. Falling back to the caller's own
   user id (already on every skill row as `user_id`, reused rather than
   invented) closes the id collision for these accounts too: two unrelated
   ungrouped members can now each have a skill called "Research", which is
   what a user expects, without needing a real tenant_ group to exist yet.
3. A platform admin gets no scope at all, deliberately: an admin is not a
   tenant customer, and a shared, flat, platform-wide id namespace for
   admin-published skills is the intended behaviour (two admin accounts
   publishing "Research" SHOULD collide, the same way a shared catalogue
   would).

The PERSISTED tenant_group_id column (composite (tenant_group_id, name)
unique index, schema in hive_skill_tenant_scope_migration.py; persisted by
models/skills.py's insert_new_skill, taught to accept it by
apply_skill_tenant_scope_model_patch.py) stays the narrow, honest fact: the
caller's real tenant group, or NULL. It is NOT widened to the user-id
fallback above, deliberately: `tenant_group_id` promises a tenant, and
`user_id` already exists on every skill row as the literal owner identity,
so overloading one column with two different meanings would mislead a
future reader rather than help one. This asymmetry is safe for the name
column too, not just the id: NULL is never equal to NULL under a UNIQUE
index (SQLite, same as Postgres and MySQL), so two ungrouped members'
skills, both with tenant_group_id NULL, already do not collide on `name`
either, with no extra column needed -- the id-scope fallback above is the
only piece that needed a change, because id is the PRIMARY KEY and its
default value (a bare slugify of name, unprefixed) was the same for
everyone with no scope, unlike the already-differentiated `name` column.

This shared instance has no tenant_id column anywhere; the only tenant
boundary object it stores is the per-tenant OWUI group the control plane
provisions at signup, named "tenant_<tenant uuid>"
(apps/control-plane/internal/signup/reconcile.go, EnsureGroup). Every
ordinary member is added to exactly one such group unconditionally, the same
call that grants them chat access at all, so that group id is what "this
account's tenant" resolves to inside this database, the same fact
apply_skill_group_grants_patch.py's _hive_filter_group_grants (issue #1396,
a separate file, untouched by this one) already relies on for a different
check.

Checked live against the demo box rather than assumed, and this is WHY case
2 above exists rather than a fail-closed 400: `SELECT u.email FROM
group_member gm JOIN user u ON u.id = gm.user_id WHERE gm.group_id = '<the
one tenant_ group that exists>'` returned exactly one row, and neither
demo@hive-demo.invalid nor the platform admin account is in ANY tenant_
group. Ungrouped is the COMMON case on this deployment today, not an edge
case. Group provisioning is a pre-existing, separate gap (most accounts on
this shared instance predate it or were never routed through the real
signup webhook), not something this patch caused. A fail-closed 400 for
case 2 would have turned this fix into an outage for the exact account the
owner demos to prospects; the user-id fallback closes the actual bug for
these accounts instead of blocking them.

What layer actually denies a cross-tenant read/write on a skill, stated
explicitly so nobody assumes database enforcement that does not exist: pure
Python application logic in routers/skills.py (ownership, AccessGrants, and
BYPASS_ADMIN_ACCESS_CONTROL being false), not a database constraint and not
row-level security. This is a plain SQLite file with no RLS concept at all.
Issue #896 (a permissive blanket-allow RLS policy) is a completely
unrelated Supabase Postgres table, account_memberships, and has no bearing
on this SQLite skills table.

Every edit asserts its own effect and the module re-parses. HIVE_OWUI_ROUTERS_DIR
overrides the target directory for scripts/test_owui_skill_tenant_scope.py.
"""

import ast
import os
import pathlib

ROUTERS = pathlib.Path(
    os.environ.get(
        "HIVE_OWUI_ROUTERS_DIR",
        "/app/backend/open_webui/routers",
    )
)

TARGET = "skills.py"
MARKER = "# hive (#1397)"

# Inserted once, right after the router is constructed -- the same anchor
# apply_skill_group_grants_patch.py's helper uses, and the literal text
# "router = APIRouter()\n" survives as a prefix of that patch's own
# insertion either way, so this anchor matches regardless of which of the
# two patches runs first.
HELPER_ANCHOR = "router = APIRouter()\n"
HELPER = '''

async def _hive_resolve_tenant_group_id(user, db):
    """Resolve the caller's tenant scope for CreateNewSkill.

    {MARKER}. This shared instance has no tenant_id column anywhere; the only
    tenant boundary object it stores is the per-tenant OWUI group the control
    plane provisions at signup, named "tenant_<tenant uuid>". Every ordinary
    member is added to exactly one such group unconditionally, so that group
    id is what "this account's tenant" resolves to here. Sorted and
    first-match rather than assuming exactly one, so behaviour stays
    deterministic if that assumption is ever loosened.

    A platform admin returns None deliberately: an admin is not a tenant
    customer, and a platform-wide skill with no tenant scope is legitimate.
    An ordinary member with no tenant_ group ALSO returns None rather than
    raising: checked live against the demo box, most existing accounts on
    this shared instance, including the one the owner demos to prospects,
    predate real tenant_ group provisioning and have none. None is safe here
    (see this module's docstring): NULL never collides with anything under
    the composite unique index this feeds.
    """
    if user.role == 'admin':
        return None

    groups = await Groups.get_groups_by_member_id(user.id, db=db)
    tenant_group_ids = sorted(g.id for g in groups if g.name.startswith('tenant_'))
    return tenant_group_ids[0] if tenant_group_ids else None


def _hive_id_scope(user, tenant_group_id):
    """The id SCOPE for CreateNewSkill, which is broader than the tenant
    group alone: tenant group if resolved, else the caller's own user id
    for an ordinary member, else None for an admin (see this module's
    docstring, case 2, for why the user-id fallback exists rather than a
    bare None for every ungrouped account).
    """
    if tenant_group_id:
        return tenant_group_id
    return None if user.role == 'admin' else user.id

'''.format(MARKER=MARKER)

ID_ANCHOR = (
    "    form_data.id = form_data.id.lower().replace(' ', '-')\n"
    "\n"
    "    existing = await Skills.get_skill_by_id(form_data.id, db=db)\n"
    "    if existing is not None:\n"
    "        raise HTTPException(\n"
    "            status_code=status.HTTP_400_BAD_REQUEST,\n"
    "            detail=ERROR_MESSAGES.ID_TAKEN,\n"
    "        )\n"
)
ID_REPLACEMENT = (
    "    # hive (#1397): scope the id to the caller's tenant instead of the\n"
    "    # whole shared instance. Two different tenants both naming a skill\n"
    "    # \"Research\" used to collide HERE first, on the id check, before name\n"
    "    # uniqueness ever mattered, since id is a bare slugify(name). See this\n"
    "    # module's docstring for the three-case priority order and why an\n"
    "    # ungrouped member (the common case here today) falls back to their\n"
    "    # own user id rather than getting no scope at all: a bare fall-through\n"
    "    # would leave every ungrouped account sharing one empty prefix, so two\n"
    "    # of them would still collide on id exactly as before this patch.\n"
    "    hive_tenant_group_id = await _hive_resolve_tenant_group_id(user, db)\n"
    "    hive_id_scope = _hive_id_scope(user, hive_tenant_group_id)\n"
    "\n"
    "    hive_base_id = form_data.id.lower().replace(' ', '-')\n"
    "    form_data.id = (\n"
    "        f'{hive_id_scope}--{hive_base_id}' if hive_id_scope else hive_base_id\n"
    "    )\n"
    "\n"
    "    existing = await Skills.get_skill_by_id(form_data.id, db=db)\n"
    "    if existing is not None:\n"
    "        raise HTTPException(\n"
    "            status_code=status.HTTP_400_BAD_REQUEST,\n"
    "            detail=ERROR_MESSAGES.ID_TAKEN,\n"
    "        )\n"
)

INSERT_CALL_ANCHOR = "skill = await Skills.insert_new_skill(user.id, form_data, db=db)\n"
INSERT_CALL_REPLACEMENT = (
    "skill = await Skills.insert_new_skill(\n"
    "            user.id, form_data, db=db, tenant_group_id=hive_tenant_group_id\n"
    "        )  # hive (#1397)\n"
)


def main():
    target = ROUTERS / TARGET
    text = target.read_text()

    if text.count(MARKER) == 3:
        print("apply_skill_tenant_scope_router_patch: already applied")
        return

    assert text.count(HELPER_ANCHOR) == 1, (
        f"{TARGET}: the router construction anchor is not present exactly once; "
        "upstream open-webui source shifted, patch needs updating"
    )
    id_found = text.count(ID_ANCHOR)
    assert id_found == 1, (
        f"{TARGET}: the id/existence-check anchor was found {id_found} times, "
        "expected 1; upstream open-webui source shifted, patch needs updating"
    )
    call_found = text.count(INSERT_CALL_ANCHOR)
    assert call_found == 1, (
        f"{TARGET}: the insert_new_skill call anchor was found {call_found} "
        "times, expected 1; upstream open-webui source shifted, patch needs "
        "updating"
    )

    patched = text.replace(HELPER_ANCHOR, HELPER_ANCHOR + HELPER, 1)
    patched = patched.replace(ID_ANCHOR, ID_REPLACEMENT, 1)
    patched = patched.replace(INSERT_CALL_ANCHOR, INSERT_CALL_REPLACEMENT, 1)

    ast.parse(patched)  # never write a router that cannot be imported
    target.write_text(patched)

    markers = patched.count(MARKER)
    assert markers == 3, f"{TARGET}: {markers} markers after patching, expected 3"
    print(
        "apply_skill_tenant_scope_router_patch: tenant-scoped the create-time "
        "id (#1397)"
    )


if __name__ == "__main__":
    main()

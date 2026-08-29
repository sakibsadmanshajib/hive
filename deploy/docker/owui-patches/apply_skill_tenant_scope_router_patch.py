"""Build-time router change for tenant-scoped skill uniqueness (issue #1397).

Router half of the #1397 fix. `id` is the primary key and is derived by
slugifying `name` (`form_data.id.lower().replace(' ', '-')`, unchanged by
this patch); it collides across tenants before name uniqueness is ever
reached, since the id check runs first in CreateNewSkill. This patch
resolves the caller's tenant OWUI group and prefixes the id with it, so an
honest slugify from two different tenants' identical display names no longer
collides on the primary key, while a genuine duplicate within the SAME
tenant still 400s ID_TAKEN, which is correct: that is real duplicate
detection, not a cross-tenant leak.

The other half of the fix, the composite (tenant_group_id, name) unique
index that replaces the single global column, is schema and lives in
hive_skill_tenant_scope_migration.py; models/skills.py's insert_new_skill is
taught to persist the resolved id by apply_skill_tenant_scope_model_patch.py.
This file is the one that resolves the id and calls it.

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

A platform admin resolves to no tenant group and gets no id prefix, matching
that same admin exemption: an admin is not a tenant customer, and a
platform-wide skill with no tenant scope is legitimate.

An ordinary member with no resolvable tenant group is treated the same way,
deliberately NOT a fail-closed 400. Checked live against the demo box rather
than assumed: `SELECT u.email FROM group_member gm JOIN user u ON u.id =
gm.user_id WHERE gm.group_id = '<the one tenant_ group that exists>'`
returned exactly one row, and neither demo@hive-demo.invalid nor the
platform admin account is in ANY tenant_ group. Group provisioning is a
pre-existing, separate gap (most accounts on this shared instance predate it
or were never routed through the real signup webhook), not something this
patch caused or should paper over with a hard block. A fail-closed 400 here
would have turned this fix into an outage for the exact account the owner
demos to prospects.

The tradeoff this leaves, stated rather than hidden: an ungrouped member's
skill gets tenant_group_id NULL, the same bucket every pre-migration legacy
row lives in, so two ungrouped members COULD share a name with no error.
That is not a new hole. NULL is never equal to NULL under the composite
unique index, so it collides with nothing, crosses no tenant boundary (an
ungrouped account has no OWUI-visible tenant to leak into or out of), and is
strictly safer than the single global column this issue is about. It is a
narrower, pre-existing usability gap (two ungrouped users could pick the
same display name), not the cross-tenant collision #1397 tracks.

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
    "    # uniqueness ever mattered, since id is a bare slugify(name). No id is\n"
    "    # left unprefixed by choice for an admin or an ungrouped member; both\n"
    "    # are None here, and the None case is not fail-closed (see this\n"
    "    # module's docstring for why a hard block would have been an outage).\n"
    "    hive_tenant_group_id = await _hive_resolve_tenant_group_id(user, db)\n"
    "\n"
    "    hive_base_id = form_data.id.lower().replace(' ', '-')\n"
    "    form_data.id = (\n"
    "        f'{hive_tenant_group_id}--{hive_base_id}' if hive_tenant_group_id else hive_base_id\n"
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

"""Build-time fix: a non-admin cannot grant a skill to a group they are not in
(issue #1396, the skills half).

Why this exists. A skill's body is appended verbatim to the chat request as a
system message (`utils/middleware.py`, the `<skill name="...">` block), so a
skill is an instruction that lands in somebody's prompt. Open WebUI's shared
grant filter, `utils/access_control.filter_allowed_access_grants`, strips only
two shapes for a non-admin: public grants, gated on `sharing.public_skills`,
and individual user grants, gated on `access_grants.allow_users`. It never
looks at group grants at all. A hand-built POST carrying
`{"principal_type": "group", "principal_id": "<another tenant's group id>"}`
is therefore stored unquestioned.

This instance is shared across every tenant and its groups are the per-tenant
groups the control plane provisions, so that grant would put attacker-authored
text into another tenant's skills list, where selecting it injects it into
that member's own turn.

Not reachable today, and that is not the reason to leave it: `GET
/api/v1/groups` filters to the caller's own memberships and the by-id route is
admin only, so a member cannot learn another tenant's group id, which are
UUIDs. That makes the defence secrecy of an identifier rather than an
authorization check, and any future surface that echoes a group id to a
non-member turns it into a live cross-tenant write with nothing else in the
way. #1396 tracks the general case across the other routers, which have the
same hole through the same shared function; this patch closes the one surface
whose payload reaches a model prompt.

Scoped to `routers/skills.py` deliberately. The shared function is called by
knowledge, models, prompts, tools, notes and folders, and changing it here
would alter grant behaviour on surfaces this change never tested.

Every edit asserts its own effect and the module re-parses, the same posture
as the other patches in this directory, so an open-webui digest bump whose
source shifted breaks the build loudly rather than silently reverting to an
unfiltered grant list. HIVE_OWUI_ROUTERS_DIR overrides the target directory
for scripts/test_owui_skill_group_grants.py.
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
MARKER = "# hive (#1396)"

# Inserted once, immediately after the router is constructed, so the three
# call sites below can reach it. `Groups` is already imported by this module.
HELPER_ANCHOR = "router = APIRouter()\n"
HELPER = '''

async def _hive_filter_group_grants(user, access_grants, db):
    """Drop group grants naming a group the caller is not a member of.

    {MARKER}. Upstream's filter_allowed_access_grants never inspects group
    grants, so without this a hand-built request can grant a skill to any
    group id on this shared instance, including another tenant's. A skill body
    reaches a prompt verbatim, so that is prompt injection across a tenant
    boundary rather than an over-broad share.

    An admin is untouched: on this deployment only a platform admin holds that
    role, and a platform-wide skill is a legitimate thing for one to publish.
    """
    if user.role == 'admin' or not access_grants:
        return access_grants

    own_group_ids = {{group.id for group in await Groups.get_groups_by_member_id(user.id, db=db)}}

    def _field(grant, name):
        if isinstance(grant, dict):
            return grant.get(name)
        return getattr(grant, name, None)

    return [
        grant
        for grant in access_grants
        if _field(grant, 'principal_type') != 'group'
        or _field(grant, 'principal_id') in own_group_ids
    ]

'''.format(MARKER=MARKER)

# The three write sites, all identical in upstream v0.10.2: create,
# id/{id}/update and id/{id}/access/update.
CALL_ANCHOR = """        form_data.access_grants,
        'sharing.public_skills',
    )
"""
CALL_REPLACEMENT = """        form_data.access_grants,
        'sharing.public_skills',
    )
    {MARKER}: and drop group grants for groups the caller is not in, which
    # filter_allowed_access_grants does not look at.
    form_data.access_grants = await _hive_filter_group_grants(
        user, form_data.access_grants, db
    )
""".format(MARKER=MARKER)

CALL_SITES = 3


def main():
    target = ROUTERS / TARGET
    text = target.read_text()

    if text.count(MARKER) == CALL_SITES + 1:
        print("apply_skill_group_grants_patch: already applied")
        return

    assert text.count(HELPER_ANCHOR) == 1, (
        f"{TARGET}: the router construction anchor is not present exactly once; "
        "upstream open-webui source shifted, patch needs updating"
    )
    assert "from open_webui.models.groups import Groups" in text, (
        f"{TARGET} no longer imports Groups, which the inserted helper closes over; "
        "patch needs updating"
    )
    found = text.count(CALL_ANCHOR)
    assert found == CALL_SITES, (
        f"{TARGET}: the access-grant filter anchor was found {found} times, "
        f"expected {CALL_SITES}; upstream open-webui source shifted, patch "
        "needs updating"
    )

    patched = text.replace(HELPER_ANCHOR, HELPER_ANCHOR + HELPER, 1)
    patched = patched.replace(CALL_ANCHOR, CALL_REPLACEMENT)

    ast.parse(patched)  # never write a router that cannot be imported
    target.write_text(patched)

    markers = patched.count(MARKER)
    assert markers == CALL_SITES + 1, (
        f"{TARGET}: {markers} markers after patching, expected {CALL_SITES + 1}"
    )
    print(
        f"apply_skill_group_grants_patch: filtered group grants at "
        f"{CALL_SITES} skill write sites (#1396)"
    )


if __name__ == "__main__":
    main()

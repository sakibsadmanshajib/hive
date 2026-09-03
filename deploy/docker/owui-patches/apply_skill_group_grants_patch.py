"""Build-time fix: a non-admin cannot grant a skill or a knowledge collection
to a group they are not in (issue #1396, the two surfaces whose payload reaches
a model prompt).

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

Scoped to `routers/skills.py` and `routers/knowledge.py` deliberately, by the
rule in the paragraph above: those are the two routers whose stored payload is
appended to somebody else's prompt, a skill body verbatim and a collection's
retrieved passages as context. `knowledge.py` joined the list in #1505, which
is the change that first let a non-admin own a collection at all and therefore
the change that first made granting one reachable. The shared function is also
called by models, prompts, tools, notes and folders, and editing it in place
would alter grant behaviour on surfaces neither change tested.

The individual-user half of the same hole is closed elsewhere and by
configuration rather than by a patch: `access_grants.allow_users` is set false
on the open-webui service and reconciled by hive_rag_env_config.py, which makes
upstream\'s own filter strip user grants from every non-admin on every router.
This file exists because that filter never inspects group grants at all.

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

# The routers to patch, and the permission key each one\'s call sites name.
# The anchor below is built from that key, so a new target is one entry.
TARGETS = {
    # (public-share permission key, number of call sites in v0.10.2)
    "skills.py": ("sharing.public_skills", 3),
    # Six rather than three: create, update and access/update as on skills,
    # plus the three external-knowledge write routes. Those three are
    # get_admin_user, so the inserted helper returns their grants untouched;
    # they are patched anyway because an anchor that matched five of six
    # occurrences would be a silent partial application.
    "knowledge.py": ("sharing.public_knowledge", 6),
}
MARKER = "# hive (#1396)"

# Inserted once, immediately after the router is constructed, so the three
# call sites below can reach it. `Groups` is already imported by this module.
HELPER_ANCHOR = "router = APIRouter()\n"
HELPER = '''

async def _hive_filter_group_grants(user, access_grants, db=None):
    """Drop group grants naming a group the caller is not a member of.

    {MARKER}. Upstream's filter_allowed_access_grants never inspects group
    grants, so without this a hand-built request can grant this resource to any
    group id on this shared instance, including another tenant's. A skill body
    reaches a prompt verbatim and a collection's passages reach one as
    retrieved context, so that is prompt injection across a tenant boundary
    rather than an over-broad share.

    An admin is untouched: on this deployment only a platform admin holds that
    role, and a platform-wide skill is a legitimate thing for one to publish.

    No session is taken from the caller. Routes differ on whether they hold
    one: knowledge.py's create deliberately declares no `db` dependency, so
    that it does not hold a connection across an embedding call, and an
    inserted call naming `db` there raised NameError and answered 500 on the
    first real capture. `Groups.get_groups_by_member_id` opens its own context
    when given None, which is exactly what `has_permission` and
    `filter_allowed_access_grants` do beside it in that same route.
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

# The write sites, all identical in upstream v0.10.2 apart from the permission
# key: create, {id}/update and {id}/access/update, plus knowledge\'s three
# external-source routes.
def call_anchor(public_key):
    return f"""        form_data.access_grants,
        '{public_key}',
    )
"""


def call_replacement(public_key):
    return call_anchor(public_key) + f"""    {MARKER}: and drop group grants for groups the caller is not in, which
    # filter_allowed_access_grants does not look at.
    form_data.access_grants = await _hive_filter_group_grants(
        user, form_data.access_grants
    )
"""


def patch_router(name, public_key, call_sites):
    target = ROUTERS / name
    text = target.read_text()

    if text.count(MARKER) == call_sites + 1:
        print(f"apply_skill_group_grants_patch: {name} already applied")
        return

    assert text.count(HELPER_ANCHOR) == 1, (
        f"{name}: the router construction anchor is not present exactly once; "
        "upstream open-webui source shifted, patch needs updating"
    )
    assert "from open_webui.models.groups import Groups" in text, (
        f"{name} no longer imports Groups, which the inserted helper closes over; "
        "patch needs updating"
    )
    anchor = call_anchor(public_key)
    found = text.count(anchor)
    assert found == call_sites, (
        f"{name}: the access-grant filter anchor was found {found} times, "
        f"expected {call_sites}; upstream open-webui source shifted, patch "
        "needs updating"
    )

    patched = text.replace(HELPER_ANCHOR, HELPER_ANCHOR + HELPER, 1)
    patched = patched.replace(anchor, call_replacement(public_key))

    ast.parse(patched)  # never write a router that cannot be imported
    target.write_text(patched)

    markers = patched.count(MARKER)
    assert markers == call_sites + 1, (
        f"{name}: {markers} markers after patching, expected {call_sites + 1}"
    )
    print(
        f"apply_skill_group_grants_patch: filtered group grants at "
        f"{call_sites} write sites in {name} (#1396)"
    )


def main():
    for name, (public_key, call_sites) in TARGETS.items():
        patch_router(name, public_key, call_sites)


if __name__ == "__main__":
    main()

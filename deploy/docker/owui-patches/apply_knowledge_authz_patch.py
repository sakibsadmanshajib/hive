"""Build-time ownership enforcement for the Knowledge by-id read routes.

Issue #1056, residual half of #947. PR #960 gated the knowledge listing
routes on BYPASS_ADMIN_ACCESS_CONTROL, but the by-id read routes still
short-circuit on user.role == 'admin': GET /{id}, GET /{id}/files (which
takes include_content and can return document text), GET /{id}/files/pending
(identical guard text to /files, patched in the same edit), and GET
/{id}/export (bare get_admin_user, no ownership check at all). On this shared
chat instance every tenant OWNER is an instance admin, so a collection id,
which is not secret, was enough to read another tenant's knowledge.

Enforcement matches the listing routes exactly (PR #960): access iff owner,
or AccessGrants read grant, or admin when BYPASS_ADMIN_ACCESS_CONTROL is set.
docker-compose.yml sets that flag false, so admins stop reading across
tenants; flipping it restores upstream behaviour.

Every edit asserts its own effect and fails the build otherwise, so an
open-webui digest bump whose source shifted breaks loudly instead of silently
reverting to cross-tenant reads. HIVE_OWUI_KNOWLEDGE_PY overrides the target
path for scripts/test_owui_knowledge_authz.py.
"""

import ast
import os
import pathlib

TARGET = pathlib.Path(
    os.environ.get(
        "HIVE_OWUI_KNOWLEDGE_PY",
        "/app/backend/open_webui/routers/knowledge.py",
    )
)

MARKER = "# hive (#1056)"

SITE_GET_OLD = "        if (\n            user.role == 'admin'\n            or knowledge.user_id == user.id\n"

SITE_FILES_OLD = "    if not (\n        user.role == 'admin'\n        or knowledge.user_id == user.id\n"

EXPORT_OLD = 'async def export_knowledge_by_id(id: str, user=Depends(get_admin_user), db: AsyncSession = Depends(get_async_session)):\n    """\n    Export a knowledge base as a zip file containing .txt files.\n    Admin only.\n    """\n\n    knowledge = await Knowledges.get_knowledge_by_id(id=id, db=db)\n    if not knowledge:\n        raise HTTPException(\n            status_code=status.HTTP_404_NOT_FOUND,\n            detail=ERROR_MESSAGES.NOT_FOUND,\n        )\n    if is_external_knowledge(knowledge):\n        external_knowledge_error()\n'
SITE_GET_NEW = "        # hive (#1056): instance admin no longer bypasses the read check here;\n        # same predicate as the listing routes (PR #960): owner, AccessGrants\n        # read grant, or admin when BYPASS_ADMIN_ACCESS_CONTROL is set.\n        if (\n            (user.role == 'admin' and BYPASS_ADMIN_ACCESS_CONTROL)\n            or knowledge.user_id == user.id\n"

SITE_FILES_NEW = "    # hive (#1056): instance admin no longer bypasses the read check here;\n    # same predicate as the listing routes (PR #960): owner, AccessGrants\n    # read grant, or admin when BYPASS_ADMIN_ACCESS_CONTROL is set.\n    if not (\n        (user.role == 'admin' and BYPASS_ADMIN_ACCESS_CONTROL)\n        or knowledge.user_id == user.id\n"

EXPORT_NEW = 'async def export_knowledge_by_id(id: str, user=Depends(get_verified_user), db: AsyncSession = Depends(get_async_session)):\n    """\n    Export a knowledge base as a zip file containing .txt files.\n\n    hive (#1056): owner, an AccessGrants read grant, or an admin when\n    BYPASS_ADMIN_ACCESS_CONTROL is explicitly set; previously the bare\n    get_admin_user dependency let any instance admin download any tenant\'s\n    collection as a zip given only its id.\n    """\n\n    knowledge = await Knowledges.get_knowledge_by_id(id=id, db=db)\n    if not knowledge:\n        raise HTTPException(\n            status_code=status.HTTP_404_NOT_FOUND,\n            detail=ERROR_MESSAGES.NOT_FOUND,\n        )\n    if is_external_knowledge(knowledge):\n        external_knowledge_error()\n\n    # hive (#1056): ownership gate replacing the bare get_admin_user\n    # dependency. Same predicate as the listing routes (PR #960).\n    if not (\n        knowledge.user_id == user.id\n        or await AccessGrants.has_access(\n            user_id=user.id,\n            resource_type=\'knowledge\',\n            resource_id=knowledge.id,\n            permission=\'read\',\n            db=db,\n        )\n        or (user.role == \'admin\' and BYPASS_ADMIN_ACCESS_CONTROL)\n    ):\n        raise HTTPException(\n            status_code=status.HTTP_401_UNAUTHORIZED,\n            detail=ERROR_MESSAGES.ACCESS_PROHIBITED,\n        )\n'

text = TARGET.read_text()
n = text.count(MARKER)
if n == 4:
    print("apply_knowledge_authz_patch: already applied")
    raise SystemExit(0)

assert text.count(SITE_GET_OLD) == 1, (
    "GET /{id} admin short-circuit not found exactly once; upstream "
    "open-webui source shifted, patch needs updating"
)
assert text.count(SITE_FILES_OLD) == 2, (
    "shared /files admin short-circuit not found exactly twice (GET "
    "/{id}/files and GET /{id}/files/pending); upstream open-webui source "
    "shifted, patch needs updating"
)
assert text.count(EXPORT_OLD) == 1, (
    "export route head not found exactly once; upstream open-webui source "
    "shifted, patch needs updating"
)

patched = text.replace(SITE_GET_OLD, SITE_GET_NEW, 1)
patched = patched.replace(SITE_FILES_OLD, SITE_FILES_NEW)
patched = patched.replace(EXPORT_OLD, EXPORT_NEW, 1)

assert patched.count(MARKER) == 4, "expected four #1056 markers after patching"
assert SITE_GET_OLD not in patched
assert SITE_FILES_OLD not in patched
assert EXPORT_OLD not in patched
ast.parse(patched)  # fails the build if the rewrite produced invalid Python

TARGET.write_text(patched)
print(
    "apply_knowledge_authz_patch: enforced ownership on GET /{id}, "
    "GET /{id}/files, GET /{id}/files/pending and GET /{id}/export"
)

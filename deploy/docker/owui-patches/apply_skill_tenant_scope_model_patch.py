"""Build-time model change for tenant-scoped skill uniqueness (issue #1397).

Companion to hive_skill_tenant_scope_migration.py (the schema half) and
apply_skill_tenant_scope_router_patch.py (the router half, which resolves
the caller's tenant and calls insert_new_skill with it). This file is the
ORM-model half: it stops asserting `unique=True` on `name` at the Python
level, since the real constraint moves to the composite
idx_skill_tenant_group_id_name index the migration creates, and it teaches
insert_new_skill to accept and persist tenant_group_id.

Deliberately additive and backward compatible: insert_new_skill's new
parameter defaults to None, so any other caller of this table's API (there
are none inside this deployment today, but the exact-literal patch posture
this directory uses assumes upstream text can move under it) keeps working
unchanged.

tenant_group_id is declared on the Skill ORM class but not on SkillModel or
any of its response subclasses, so it is a DB-only column: pydantic's
from_attributes validation only pulls the fields a model declares, and none
of the Skill response schemas declare this one. It never reaches an API
response.

Every edit asserts its own effect and the module re-parses, the same posture
as the rest of this directory, so an open-webui digest bump whose source
shifted breaks the build loudly instead of silently reverting to the
unscoped global name column. HIVE_OWUI_MODELS_DIR overrides the target
directory for scripts/test_owui_skill_tenant_scope.py.
"""

import ast
import os
import pathlib

MODELS = pathlib.Path(
    os.environ.get(
        "HIVE_OWUI_MODELS_DIR",
        "/app/backend/open_webui/models",
    )
)

TARGET = "skills.py"
MARKER = "# hive (#1397)"

COLUMN_ANCHOR = (
    "    id = Column(String, primary_key=True, unique=True)\n"
    "    user_id = Column(String)\n"
    "    name = Column(Text, unique=True)\n"
)
COLUMN_REPLACEMENT = (
    "    id = Column(String, primary_key=True, unique=True)\n"
    "    user_id = Column(String)\n"
    "    # hive (#1397): tenant scope for the composite name-uniqueness index\n"
    "    # idx_skill_tenant_group_id_name (see the migration of the same name);\n"
    "    # not declared on SkillModel, so it never reaches an API response.\n"
    "    tenant_group_id = Column(String, nullable=True)\n"
    "    # hive (#1397): DB-level uniqueness on name alone moved to the\n"
    "    # composite index above; a single global name column is exactly the\n"
    "    # cross-tenant collision this issue is about.\n"
    "    name = Column(Text)\n"
)

INSERT_SIGNATURE_ANCHOR = (
    "    async def insert_new_skill(\n"
    "        self,\n"
    "        user_id: str,\n"
    "        form_data: SkillForm,\n"
    "        db: Optional[AsyncSession] = None,\n"
    "    ) -> Optional[SkillModel]:\n"
    "        async with get_async_db_context(db) as db:\n"
    "            try:\n"
    "                result = Skill(\n"
    "                    **{\n"
    "                        **form_data.model_dump(exclude={'access_grants'}),\n"
    "                        'user_id': user_id,\n"
    "                        'updated_at': int(time.time()),\n"
    "                        'created_at': int(time.time()),\n"
    "                    }\n"
    "                )\n"
)
INSERT_SIGNATURE_REPLACEMENT = (
    "    async def insert_new_skill(\n"
    "        self,\n"
    "        user_id: str,\n"
    "        form_data: SkillForm,\n"
    "        db: Optional[AsyncSession] = None,\n"
    "        tenant_group_id: Optional[str] = None,  # hive (#1397)\n"
    "    ) -> Optional[SkillModel]:\n"
    "        async with get_async_db_context(db) as db:\n"
    "            try:\n"
    "                result = Skill(\n"
    "                    **{\n"
    "                        **form_data.model_dump(exclude={'access_grants'}),\n"
    "                        'user_id': user_id,\n"
    "                        'tenant_group_id': tenant_group_id,\n"
    "                        'updated_at': int(time.time()),\n"
    "                        'created_at': int(time.time()),\n"
    "                    }\n"
    "                )\n"
)

EXPECTED_MARKERS = 3


def main():
    target = MODELS / TARGET
    text = target.read_text()

    if text.count(MARKER) == EXPECTED_MARKERS:
        print("apply_skill_tenant_scope_model_patch: already applied")
        return

    for anchor, replacement in (
        (COLUMN_ANCHOR, COLUMN_REPLACEMENT),
        (INSERT_SIGNATURE_ANCHOR, INSERT_SIGNATURE_REPLACEMENT),
    ):
        found = text.count(anchor)
        assert found == 1, (
            f"{TARGET}: anchor found {found} times, expected 1; upstream "
            f"open-webui source shifted, patch needs updating. "
            f"Anchor head: {anchor[:90]!r}"
        )
        text = text.replace(anchor, replacement)

    ast.parse(text)  # never write a model module that cannot be imported
    target.write_text(text)

    markers = text.count(MARKER)
    assert markers == EXPECTED_MARKERS, (
        f"{TARGET}: {markers} markers after patching, expected {EXPECTED_MARKERS}"
    )
    print(
        f"apply_skill_tenant_scope_model_patch: added tenant_group_id and "
        f"dropped the single-column unique on name (#1397)"
    )


if __name__ == "__main__":
    main()

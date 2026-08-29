"""Add tenant_group_id to skill table and scope name uniqueness to it (hive #1397)

Revision ID: c3f8a2b91d07
Revises: d4e5f6a7b8c9
Create Date: 2026-08-29 00:00:00.000000

Not an upstream file. Dropped into this deployment's migrations/versions
directory by deploy/docker/Dockerfile.open-webui (search "issue #1397, the
migration half"), which is why this docstring, unlike a real upstream
migration's, explains WHY rather than just WHAT.

The problem (issue #1397): a1b2c3d4e5f6_add_skill_table.py declared
`name = Column(Text, unique=True)`, a single global column, on a chat
instance shared by every Hive tenant. The first tenant to create a skill
named "Research" takes that name for everyone; the second tenant's identical
create 400s. `id`, the primary key, collides even earlier, since
routers/skills.py derives it by slugifying `name` before this migration's
own patch (apply_skill_tenant_scope_router_patch.py) starts prefixing it, so
two tenants naming a skill the same thing collided on the PK check before
name uniqueness was ever reached.

down_revision points at d4e5f6a7b8c9 (add_automation_tables), the true head
of this deployment's migration chain: it is the one revision id in this
directory that no other file lists as its own down_revision. If that
assumption is ever wrong (a later migration exists in the real pinned image
that this vendored mirror does not carry), Alembic's own upgrade command
fails loudly with "Multiple head revisions are present", not silently -- this
migration cannot both apply cleanly and leave the chain broken.

Why raw SQL instead of op.batch_alter_table. SQLite implements an inline
`Column(..., unique=True)` as an unnamed autoindex Alembic cannot target with
op.drop_constraint, and batch mode's "recreate the table" behaviour depends
on how SQLAlchemy reflects that autoindex back, which was not something this
change wanted to rely on discovering correctly for a live production
database. The raw rebuild below is the same three-step SQLite pattern batch
mode performs internally (create the new shape, copy every row, swap names),
made explicit so it is exactly what
scripts/test_owui_skill_tenant_scope.py proves against a real sqlite3
connection before this file is trusted anywhere close to production data.

What happens to rows that already exist (checked, not assumed: see this
issue's PR body for the live row count on the demo box at merge time).
add_column with no server_default leaves every existing row's
tenant_group_id NULL. That is deliberate, not a gap: SQLite (like Postgres
and MySQL) never considers NULL equal to NULL under a UNIQUE index, so a
pre-existing row collides with nothing, before or after this migration, and
needs no rename and no guessed tenant assignment for this migration to
succeed. tenant_group_id is not consulted by any read or write access-control
check in this deployment (those are user_id and AccessGrants, both untouched
here); it only feeds the id prefix and the composite uniqueness check that
routers/skills.py's create path applies going forward.
"""

from typing import Sequence, Union

from alembic import op
from open_webui.migrations.util import get_existing_tables

revision: str = 'c3f8a2b91d07'
down_revision: Union[str, None] = 'd4e5f6a7b8c9'
branch_labels: Union[str, Sequence[str], None] = None
depends_on: Union[str, Sequence[str], None] = None


# Kept in sync by hand with scripts/test_owui_skill_tenant_scope.py, which
# runs this exact statement block against a real sqlite3 connection (an old
# schema built from a1b2c3d4e5f6_add_skill_table.py's own DDL, seeded with a
# pre-existing row) before every claim below is trusted.
REBUILD_SQL = """
CREATE TABLE skill_new (
    id VARCHAR NOT NULL PRIMARY KEY,
    user_id VARCHAR NOT NULL,
    name TEXT NOT NULL,
    description TEXT,
    content TEXT NOT NULL,
    meta JSON,
    is_active BOOLEAN NOT NULL,
    updated_at BIGINT NOT NULL,
    created_at BIGINT NOT NULL,
    tenant_group_id VARCHAR
);
INSERT INTO skill_new (id, user_id, name, description, content, meta, is_active, updated_at, created_at, tenant_group_id)
SELECT id, user_id, name, description, content, meta, is_active, updated_at, created_at, NULL FROM skill;
DROP TABLE skill;
ALTER TABLE skill_new RENAME TO skill;
CREATE INDEX idx_skill_user_id ON skill (user_id);
CREATE INDEX idx_skill_updated_at ON skill (updated_at);
CREATE UNIQUE INDEX idx_skill_tenant_group_id_name ON skill (tenant_group_id, name);
"""


def upgrade() -> None:
    existing_tables = set(get_existing_tables())
    if 'skill' not in existing_tables:
        # First-ever boot of a deployment new enough that this migration
        # ships alongside a1b2c3d4e5f6_add_skill_table.py in the same
        # alembic run: that migration's own upgrade() creates the table
        # fresh in this same transaction, so there is nothing here to
        # rebuild yet, and this migration is a no-op on that path.
        return

    for statement in REBUILD_SQL.strip().split(';\n'):
        statement = statement.strip()
        if statement:
            op.execute(statement)


def downgrade() -> None:
    op.execute('DROP INDEX IF EXISTS idx_skill_tenant_group_id_name')
    op.execute(
        """
        CREATE TABLE skill_old (
            id VARCHAR NOT NULL PRIMARY KEY,
            user_id VARCHAR NOT NULL,
            name TEXT NOT NULL UNIQUE,
            description TEXT,
            content TEXT NOT NULL,
            meta JSON,
            is_active BOOLEAN NOT NULL,
            updated_at BIGINT NOT NULL,
            created_at BIGINT NOT NULL
        )
        """
    )
    op.execute(
        'INSERT INTO skill_old (id, user_id, name, description, content, meta, '
        'is_active, updated_at, created_at) '
        'SELECT id, user_id, name, description, content, meta, is_active, '
        'updated_at, created_at FROM skill'
    )
    op.execute('DROP TABLE skill')
    op.execute('ALTER TABLE skill_old RENAME TO skill')
    op.execute('CREATE INDEX idx_skill_user_id ON skill (user_id)')
    op.execute('CREATE INDEX idx_skill_updated_at ON skill (updated_at)')

#!/usr/bin/env python3
"""Self-check for how a login's Open WebUI role is decided (issue #748).

Open WebUI is one instance shared by every Hive tenant, and it used to derive
its instance-admin role from a Hive tenant role: a single ACTIVE
`tenant_users` row with `role = 'OWNER'` became Open WebUI `admin`. A tenant
OWNER is a customer, so that handed one customer administrative control of
every other customer's chat. Instance admin now comes only from the control
plane's explicit platform-level attribute (`accounts.is_platform_admin` plus an
ACTIVE `owner` row in `account_memberships`, the same predicate
`apps/control-plane/internal/platform/role_pgx.go` uses).

Three things are checked here, and it is worth being precise about which of
them would have caught the old behaviour, because "a test exists" is not the
same as "a test can fail":

  1. Structural, and this is the one that fails on the old fragment: the SQL
     the fragment executes must ask about `is_platform_admin`, and the fragment
     must not compare a tenant role against 'OWNER' anywhere.
  2. Behavioural, over a stubbed psycopg2: the branch logic (operator, member,
     nobody, ambiguous, raising) resolves the role this deployment intends, and
     fails closed on everything it cannot answer.
  3. The build-time rewrite: running the real `apply_tenant_role_patch.py`
     against a copy of the pinned image's own `oauth.py` removes upstream's two
     single-user admin promotions, splices the fragment, and leaves a file that
     still compiles.

What this file cannot check is the SQL's meaning against a real database. That
is covered by `apps/control-plane/internal/tenants/access_token_hook_test.go`
(database backed) for the claim half, and by the live capture committed under
`docs/proof/` for the lookup half.

No framework, no network, no Open WebUI import.
Run: python3 scripts/test_owui_tenant_role.py
"""

import os
import py_compile
import subprocess
import sys
import tempfile
import types
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[1]
PATCHES = REPO_ROOT / "deploy" / "docker" / "owui-patches"
FRAGMENT_PATH = PATCHES / "tenant_role_from_db.py"
APPLY_PATH = PATCHES / "apply_tenant_role_patch.py"
# Byte-identical to /app/backend/open_webui/utils/oauth.py in
# ghcr.io/open-webui/open-webui:v0.10.2 (the digest deploy/docker/
# Dockerfile.open-webui pins), verified with `docker run --entrypoint cat`.
# The vendored copy is used here because the image is 7 GB and this check runs
# on every pull request; the build itself runs the same script against the
# image, which is what makes a drift between the two fail loudly.
VENDORED_OAUTH = (
    REPO_ROOT / "vendor" / "open-webui" / "backend" / "open_webui" / "utils" / "oauth.py"
)

FRAGMENT_SOURCE = FRAGMENT_PATH.read_text(encoding="utf-8")


class _Cursor:
    """Records every statement, answers the one SELECT with canned rows."""

    def __init__(self, rows, raise_on_select=None):
        self._rows = rows
        self._raise_on_select = raise_on_select
        self.statements = []
        self.parameters = []

    def __enter__(self):
        return self

    def __exit__(self, *_):
        return False

    def execute(self, statement, parameters=None):
        self.statements.append(statement)
        self.parameters.append(parameters)
        if statement.lstrip().upper().startswith("SELECT") and self._raise_on_select:
            raise self._raise_on_select

    def fetchall(self):
        return self._rows


class _Connection:
    def __init__(self, cursor):
        self._cursor = cursor
        self.closed = False

    def cursor(self):
        return self._cursor

    def close(self):
        self.closed = True


class _Log:
    def __init__(self):
        self.warnings = []

    def warning(self, message):
        self.warnings.append(message)


class _User:
    def __init__(self, email):
        self.email = email


def run_fragment(
    rows,
    *,
    fallback_role="pending",
    email="someone@example.invalid",
    db_url="postgresql://stub/stub",
    raise_on_connect=None,
    raise_on_select=None,
):
    """Execute the real fragment with a stubbed psycopg2 and return the outcome.

    The fragment is a source excerpt spliced into `get_user_role`'s body at
    build time, so it is exercised the same way: `exec` against a namespace
    holding the locals it reads (`user`, `user_data`, `role`, `log`).
    """
    cursor = _Cursor(rows, raise_on_select=raise_on_select)
    connection = _Connection(cursor)

    def connect(dsn, connect_timeout=None):
        if raise_on_connect:
            raise raise_on_connect
        connect.calls.append((dsn, connect_timeout))
        return connection

    connect.calls = []

    stub = types.ModuleType("psycopg2")
    stub.connect = connect

    log = _Log()
    namespace = {
        "user": _User(email) if email is not None else None,
        "user_data": {"email": email} if email is not None else {},
        "role": fallback_role,
        "log": log,
    }

    previous_module = sys.modules.get("psycopg2")
    previous_url = os.environ.get("PGVECTOR_DB_URL")
    sys.modules["psycopg2"] = stub
    if db_url is None:
        os.environ.pop("PGVECTOR_DB_URL", None)
    else:
        os.environ["PGVECTOR_DB_URL"] = db_url
    try:
        exec(compile(FRAGMENT_SOURCE, str(FRAGMENT_PATH), "exec"), namespace)
    finally:
        if previous_module is None:
            sys.modules.pop("psycopg2", None)
        else:
            sys.modules["psycopg2"] = previous_module
        if previous_url is None:
            os.environ.pop("PGVECTOR_DB_URL", None)
        else:
            os.environ["PGVECTOR_DB_URL"] = previous_url

    return {
        "role": namespace["role"],
        "cursor": cursor,
        "connection": connection,
        "log": log,
    }


# --- 1. Structural: the decision's inputs -----------------------------------


def test_the_lookup_asks_the_control_plane_for_platform_admin() -> None:
    """The old fragment asked `tenant_users` for a role and mapped OWNER onto
    admin. This assertion is what fails against that version."""
    outcome = run_fragment([(False, True)])
    select = [s for s in outcome["cursor"].statements if s.lstrip().upper().startswith("SELECT")]
    assert len(select) == 1, f"expected exactly one SELECT, got {len(select)}"
    sql = select[0]
    assert "is_platform_admin" in sql, (
        "instance admin must be resolved from the control plane's explicit "
        "platform attribute, not inferred from anything else"
    )
    assert "public.account_memberships" in sql and "public.accounts" in sql, (
        "the platform-admin predicate must match the control plane's own "
        "(apps/control-plane/internal/platform/role_pgx.go)"
    )
    assert "m.role = 'owner'" in sql and "m.status = 'active'" in sql, (
        "an invited-but-inactive membership on a platform-admin account must "
        "not confer anything, same as the Go predicate"
    )


def test_no_tenant_role_is_compared_against_owner_anywhere() -> None:
    """A tenant role must not be able to decide any role in this fragment, so
    the comparison itself must not exist. Comments are exempt: this file's
    header describes the removed behaviour on purpose."""
    code = "\n".join(
        line for line in FRAGMENT_SOURCE.splitlines() if not line.lstrip().startswith("#")
    )
    for forbidden in ("'OWNER'", '"OWNER"'):
        assert forbidden not in code, (
            f"the fragment still tests a tenant role against {forbidden}. Instance "
            "admin is a platform attribute, never a tenant role (issue #748)"
        )


def test_membership_check_excludes_archived_tenants() -> None:
    outcome = run_fragment([(False, True)])
    sql = outcome["cursor"].statements[-1]
    assert "t.archived_at IS NULL" in sql, (
        "chat access must not survive the tenant being archived, matching "
        "public.custom_access_token_hook's own membership snapshot"
    )


def test_the_email_is_a_bound_parameter() -> None:
    hostile = "victim@example.invalid' OR '1'='1"
    outcome = run_fragment([(False, True)], email=hostile)
    cursor = outcome["cursor"]
    select_index = [
        i for i, s in enumerate(cursor.statements) if s.lstrip().upper().startswith("SELECT")
    ][0]
    assert cursor.parameters[select_index] == (hostile,), (
        "the email must reach the driver as a bound parameter, never spliced "
        "into the statement"
    )
    assert hostile not in cursor.statements[select_index]


def test_the_lookup_stays_bounded() -> None:
    """Every login runs this. The two SET LOCAL statements are what keep a
    stalled database from holding the sign-in open."""
    outcome = run_fragment([(False, True)])
    statements = " ".join(outcome["cursor"].statements)
    assert "SET LOCAL statement_timeout" in statements
    assert "SET LOCAL lock_timeout" in statements
    assert outcome["connection"].closed, "the connection must be closed on every path"


# --- 2. Behavioural: the branch logic ---------------------------------------


def test_a_platform_operator_gets_instance_admin() -> None:
    assert run_fragment([(True, True)])["role"] == "admin"


def test_a_platform_operator_with_no_tenant_membership_still_gets_admin() -> None:
    """An operator's workspace need not be a chat tenant."""
    assert run_fragment([(True, False)])["role"] == "admin"


def test_an_ordinary_tenant_member_gets_the_user_role() -> None:
    assert run_fragment([(False, True)])["role"] == "user"


def test_an_identity_with_no_active_membership_keeps_the_fallback() -> None:
    """Fallback is DEFAULT_USER_ROLE, "pending" on this deployment, which is
    the activation screen. No membership must mean no access, not some access."""
    assert run_fragment([(False, False)])["role"] == "pending"


def test_an_unknown_email_keeps_the_fallback() -> None:
    assert run_fragment([])["role"] == "pending"


def test_an_ambiguous_identity_keeps_the_fallback() -> None:
    """Two auth.users rows for one address is not something to resolve toward
    privilege. Note the rows say operator: the point is that they are ignored."""
    assert run_fragment([(True, True), (True, True)])["role"] == "pending"


def test_a_failing_lookup_keeps_the_fallback_and_warns() -> None:
    outcome = run_fragment([], raise_on_connect=OSError("connection refused"))
    assert outcome["role"] == "pending"
    assert outcome["log"].warnings, "a failed lookup must be logged, not swallowed"
    assert "connection refused" in outcome["log"].warnings[0]


def test_a_query_that_raises_keeps_the_fallback() -> None:
    outcome = run_fragment([], raise_on_select=RuntimeError("statement timeout"))
    assert outcome["role"] == "pending"
    assert outcome["log"].warnings


def test_a_missing_database_url_changes_nothing() -> None:
    """Any deployment without PGVECTOR_DB_URL keeps Open WebUI's own answer."""
    assert run_fragment([(True, True)], db_url=None)["role"] == "pending"


def test_a_failure_can_never_leave_an_admin_fallback_standing() -> None:
    """The fallback is DEFAULT_USER_ROLE, which a deployment chooses. If one
    chooses `admin`, an unreachable database must not promote every login while
    the check that decides admin is the thing that failed."""
    outcome = run_fragment(
        [], fallback_role="admin", raise_on_connect=OSError("connection refused")
    )
    assert outcome["role"] == "user", outcome["role"]
    assert outcome["log"].warnings


def test_an_already_resolved_role_is_never_upgraded_by_a_failure() -> None:
    """If the OAuth claim machinery above already resolved 'user', a failing
    lookup must leave it there rather than reaching for anything better."""
    outcome = run_fragment(
        [], fallback_role="user", raise_on_connect=OSError("connection refused")
    )
    assert outcome["role"] == "user"


# --- 3. The build-time rewrite ----------------------------------------------


def _patched_oauth_source() -> str:
    with tempfile.TemporaryDirectory() as tmp:
        target = Path(tmp) / "oauth.py"
        target.write_text(VENDORED_OAUTH.read_text(encoding="utf-8"), encoding="utf-8")
        environment = dict(os.environ)
        environment["HIVE_OWUI_OAUTH_PY"] = str(target)
        environment["HIVE_TENANT_ROLE_FRAGMENT"] = str(FRAGMENT_PATH)
        result = subprocess.run(
            [sys.executable, str(APPLY_PATH)],
            env=environment,
            capture_output=True,
            text=True,
        )
        assert result.returncode == 0, (
            "apply_tenant_role_patch.py failed against the pinned image's own "
            f"oauth.py:\n{result.stderr}"
        )
        py_compile.compile(str(target), doraise=True)
        return target.read_text(encoding="utf-8")


def test_the_rewrite_removes_upstreams_login_time_admin_promotion() -> None:
    """Upstream promotes the only user of the instance to admin on every login,
    above the splice point, so the Hive lookup never ran for that login. On a
    shared instance that is whoever authenticates first after a volume reset."""
    patched = _patched_oauth_source()
    assert "Assigning the only user the admin role" not in patched
    assert "if user and user_count == 1:" not in patched


def test_the_rewrite_removes_upstreams_post_insert_admin_promotion() -> None:
    patched = _patched_oauth_source()
    assert "await Users.update_user_role_by_id(user.id, 'admin', db=db)" not in patched
    assert "if await Users.get_num_users(db=db) == 1:" not in patched


def test_the_rewrite_splices_the_tenant_lookup() -> None:
    patched = _patched_oauth_source()
    assert "hive tenant-role lookup failed" in patched
    assert "is_platform_admin" in patched


def test_the_rewrite_leaves_the_claim_machinery_alone() -> None:
    """Only the tenancy-derived and bootstrap grants go. Open WebUI's own
    OAUTH_ADMIN_ROLES path is deployment configuration, not tenancy, and the
    fragment runs after it, so a resolvable identity still overrides it."""
    patched = _patched_oauth_source()
    assert "oauth_admin_roles" in patched
    assert "if not user and user_count == 0:" in patched


def test_the_build_still_asserts_the_patch_landed() -> None:
    dockerfile = (REPO_ROOT / "deploy" / "docker" / "Dockerfile.open-webui").read_text(
        encoding="utf-8"
    )
    assert "apply_tenant_role_patch.py" in dockerfile, "the build must run the patch"
    assert (
        "grep -q 'hive tenant-role lookup failed'" in dockerfile
    ), "the build must assert the splice landed"


def main() -> None:
    for name, fn in sorted(globals().items()):
        if name.startswith("test_") and callable(fn):
            fn()
    print("ok: owui instance admin comes from the platform attribute, not a tenant role")


if __name__ == "__main__":
    sys.exit(main())

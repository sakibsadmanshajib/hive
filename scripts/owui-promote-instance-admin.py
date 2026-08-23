#!/usr/bin/env python3
"""Promote one Open WebUI account to instance admin, inside the container.

Issue #748 removed every way an Open WebUI instance admin could appear by
itself: a tenant OWNER is no longer mapped onto it, and upstream's
first-user-becomes-admin promotions are deleted. On a real deployment the
replacement is deliberate and lives in the control plane
(`public.accounts.is_platform_admin` plus an ACTIVE `owner` row in
`public.account_memberships`, resolved on every login by
`deploy/docker/owui-patches/tenant_role_from_db.py`).

A throwaway test stack is the one case where that replacement is the wrong
lever. The nightly Open WebUI suite needs an admin session for exactly one
thing, installing the `hive_jwt_forward` Function, and its Open WebUI container
is created and destroyed inside the job. Granting its fixture account
`is_platform_admin` would hand a CI fixture the platform-wide authority behind
credit minting and provider base-URL rewrites, in whatever database that job
points at, which is the hazard issue #747 is about. So the fixture's admin
comes from here instead: a row in the disposable container's own database, with
no Hive authority attached to it at all.

Deliberately unstable, and that is the point. The OAuth login path re-resolves
the role on every login and writes back any difference, so this promotion is
undone the next time that account signs in. It is a bootstrap for one job, not
a way to keep an administrator.

Run it INSIDE the container, the same way scripts/owui-mint-admin-token.py is
run, because it reads the server's own database location out of the server's
own process environment:

  docker compose exec -T -e OWUI_PROMOTE_EMAIL=<address> open-webui \\
    python3 - < scripts/owui-promote-instance-admin.py

Creates nothing. The account has to exist already, which means it has to have
signed in at least once, so a missing row is an error rather than an insert:
inventing an account here would produce one Open WebUI knows nothing about.

Self-check (no container, no database of its own):
  python3 scripts/owui-promote-instance-admin.py --self-check
"""
import os
import sqlite3
import sys
import tempfile
from pathlib import Path

TARGET_ROLE = "admin"


def server_environment() -> dict[str, str]:
    """The environment of the Open WebUI server process.

    Same approach as scripts/owui-mint-admin-token.py: DATA_DIR and
    DATABASE_URL are read from the running server rather than assumed, and every
    /proc entry is scanned rather than assuming the server is PID 1, so this
    keeps working under an init shim or a wrapper entrypoint.
    """
    for entry in sorted(Path("/proc").iterdir(), key=lambda p: p.name):
        if not entry.name.isdigit():
            continue
        try:
            raw = (entry / "environ").read_bytes()
        except OSError:
            continue
        found = {}
        for pair in raw.split(b"\0"):
            if not pair or b"=" not in pair:
                continue
            key, _, value = pair.partition(b"=")
            found[key.decode("utf-8", "replace")] = value.decode("utf-8", "replace")
        if found.get("WEBUI_SECRET_KEY"):
            return found
    raise SystemExit(
        "no running process exposes WEBUI_SECRET_KEY. Run this inside the "
        "open-webui container, while it is up."
    )


def sqlite_path(environment: dict[str, str]) -> Path:
    database_url = environment.get("DATABASE_URL", "").strip()
    if not database_url:
        data_dir = environment.get("DATA_DIR", "/app/backend/data")
        return Path(data_dir) / "webui.db"
    if not database_url.startswith("sqlite:///"):
        raise SystemExit(
            "Open WebUI is not on sqlite (DATABASE_URL is set to a "
            f"{database_url.split(':', 1)[0]} URL). This script writes the role "
            "straight into the sqlite file; teach it the other backend before "
            "pointing a test stack at one."
        )
    return Path(database_url[len("sqlite:///") :])


def promote(database: Path, email: str) -> str:
    """Set one account's role to admin. Returns the role it held before.

    Matched case-insensitively because Open WebUI lower-cases an address on
    provisioning while a caller may pass whatever the fixture printed.
    """
    if not database.exists():
        raise SystemExit(f"Open WebUI database not found at {database}")
    connection = sqlite3.connect(database)
    try:
        row = connection.execute(
            'SELECT id, role FROM "user" WHERE lower(email) = lower(?)', (email,)
        ).fetchone()
        if row is None:
            raise SystemExit(
                f"no Open WebUI account for {email}. It has to sign in once "
                "before it can be promoted: this script creates nothing, "
                "because an account Open WebUI did not provision is not one it "
                "can authenticate."
            )
        user_id, previous = row
        connection.execute(
            'UPDATE "user" SET role = ? WHERE id = ?', (TARGET_ROLE, user_id)
        )
        connection.commit()
    finally:
        connection.close()
    return previous


def self_check() -> int:
    with tempfile.TemporaryDirectory() as tmp:
        database = Path(tmp) / "webui.db"
        connection = sqlite3.connect(database)
        connection.execute('CREATE TABLE "user" (id TEXT, email TEXT, role TEXT)')
        connection.execute(
            'INSERT INTO "user" VALUES (?, ?, ?)', ("u1", "Fixture@Example.Invalid", "user")
        )
        connection.execute(
            'INSERT INTO "user" VALUES (?, ?, ?)', ("u2", "other@example.invalid", "user")
        )
        connection.commit()
        connection.close()

        previous = promote(database, "fixture@example.invalid")
        assert previous == "user", previous

        connection = sqlite3.connect(database)
        roles = dict(connection.execute('SELECT id, role FROM "user"').fetchall())
        connection.close()
        assert roles["u1"] == "admin", roles
        assert roles["u2"] == "user", "only the named account may be promoted"

        # A second run is a no-op rather than an error: the nightly re-runs.
        assert promote(database, "fixture@example.invalid") == "admin"

        try:
            promote(database, "nobody@example.invalid")
        except SystemExit as error:
            assert "has to sign in once" in str(error), error
        else:
            raise AssertionError("a missing account must fail rather than insert one")

        missing = Path(tmp) / "absent.db"
        try:
            promote(missing, "fixture@example.invalid")
        except SystemExit as error:
            assert "not found" in str(error), error
        else:
            raise AssertionError("a missing database must fail loudly")

    print("owui-promote-instance-admin self-check: OK (5 cases)")
    return 0


def main() -> int:
    if "--self-check" in sys.argv:
        return self_check()

    email = os.environ.get("OWUI_PROMOTE_EMAIL", "").strip()
    if not email:
        raise SystemExit("OWUI_PROMOTE_EMAIL is empty; nothing to promote")

    environment = server_environment()
    previous = promote(sqlite_path(environment), email)
    print(f"{email}: role {previous} -> {TARGET_ROLE} (this instance only)")
    return 0


if __name__ == "__main__":
    sys.exit(main())

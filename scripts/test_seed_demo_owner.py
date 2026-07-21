#!/usr/bin/env python3
"""Self-check for the slug-collision guards in seed-demo-owner.py (P1 fix:
Greptile review on PR #416 flagged that an unguarded on_conflict=slug upsert
would silently elevate an unrelated pre-existing tenant/account to
demo-owner privilege). No framework, no network: exercises the two pure
guard functions directly. Run: python3 scripts/test_seed_demo_owner.py
"""
import importlib.util
import sys
from pathlib import Path

spec = importlib.util.spec_from_file_location(
    "seed_demo_owner", Path(__file__).parent / "seed-demo-owner.py"
)
seed_demo_owner = importlib.util.module_from_spec(spec)
spec.loader.exec_module(seed_demo_owner)


def exits(fn, *args) -> bool:
    try:
        fn(*args)
    except SystemExit as e:
        return e.code == 1
    return False


def main() -> None:
    # No existing row at all: never a collision, regardless of members/owners.
    assert not exits(seed_demo_owner.guard_tenant_slug, None, [])
    assert not exits(seed_demo_owner.guard_account_slug, None, [])

    # Existing tenant whose only member is our own demo user: safe re-run.
    assert not exits(
        seed_demo_owner.guard_tenant_slug, {"id": "t1"}, []
    )

    # Existing tenant with a foreign member: real customer tenant, must fail.
    assert exits(
        seed_demo_owner.guard_tenant_slug,
        {"id": "t1"},
        [{"user_id": "someone-else"}],
    )

    # Existing account whose only owner-role member is our own demo user:
    # safe re-run (a co-owner with role='member' would not appear here at
    # all -- only role='owner' rows are queried, matching IsPlatformAdmin).
    assert not exits(
        seed_demo_owner.guard_account_slug, {"id": "a1"}, []
    )

    # Existing account with a second owner-role member: control-plane's
    # IsPlatformAdmin (role_pgx.go) authorizes ANY owner-role membership on
    # an is_platform_admin account, so this second owner would silently
    # gain platform-admin too -- must fail, not merge.
    assert exits(
        seed_demo_owner.guard_account_slug,
        {"id": "a1"},
        [{"user_id": "someone-else"}],
    )

    print("ok: seed-demo-owner.py slug-collision guards")


if __name__ == "__main__":
    main()

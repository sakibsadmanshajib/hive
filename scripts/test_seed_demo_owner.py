#!/usr/bin/env python3
"""Self-check for the slug-collision guards in seed-demo-owner.py (P1 fix:
Greptile review on PR #416 flagged that an unguarded on_conflict=slug upsert
would silently elevate an unrelated pre-existing tenant/account to
demo-owner privilege; issue #420 is the fast-follow closing the two edge
cases PR #416's review left open: owner_user_id desync and zero-membership
tenant collision). No framework, no network: exercises the two pure guard
functions directly. Run: python3 scripts/test_seed_demo_owner.py
"""
import importlib.util
import os
import sys
from pathlib import Path

spec = importlib.util.spec_from_file_location(
    "seed_demo_owner", Path(__file__).parent / "seed-demo-owner.py"
)
seed_demo_owner = importlib.util.module_from_spec(spec)
# The identity constants resolve at import time, so drop any override the
# caller's shell happens to export -- this file asserts the defaults, and an
# operator provisioning a second owner exports exactly these variables.
for _override in (
    "HIVE_DEMO_EMAIL",
    "HIVE_DEMO_TENANT_SLUG",
    "HIVE_DEMO_TENANT_NAME",
    "HIVE_DEMO_ACCOUNT_SLUG",
    "HIVE_DEMO_ACCOUNT_NAME",
):
    os.environ.pop(_override, None)
spec.loader.exec_module(seed_demo_owner)


def exits(fn, *args) -> bool:
    try:
        fn(*args)
    except SystemExit as e:
        return e.code == 1
    return False


def main() -> None:
    # No existing row at all: never a collision, regardless of members/owners.
    assert not exits(seed_demo_owner.guard_tenant_slug, None, [], False)
    assert not exits(seed_demo_owner.guard_account_slug, None, [], "demo-user")

    # (c) Legitimate case: existing tenant whose only member is our own demo
    # user (own_member=True, no foreign members) -- a prior run of this
    # exact script created it. Safe re-run, idempotency must not regress.
    assert not exits(
        seed_demo_owner.guard_tenant_slug, {"id": "t1"}, [], True
    )

    # Existing tenant with a foreign member: real customer tenant, must fail
    # regardless of own_member.
    assert exits(
        seed_demo_owner.guard_tenant_slug,
        {"id": "t1"},
        [{"user_id": "someone-else"}],
        True,
    )

    # (b) issue #420 gap 2: a pre-existing tenant with ZERO memberships at
    # all (never seeded by this script, or fully cleaned up) has no foreign
    # members either -- own_member=False must still refuse it, not adopt it.
    assert exits(
        seed_demo_owner.guard_tenant_slug,
        {"id": "t1"},
        [],
        False,
    )

    # (c) Legitimate case: existing account whose owner_user_id already
    # matches our demo user and no other owner-role member exists -- safe
    # re-run (a co-owner with role='member' would not appear here at all --
    # only role='owner' rows are queried, matching IsPlatformAdmin).
    assert not exits(
        seed_demo_owner.guard_account_slug,
        {"id": "a1", "owner_user_id": "demo-user"},
        [],
        "demo-user",
    )

    # Existing account with a second owner-role member: control-plane's
    # IsPlatformAdmin (role_pgx.go) authorizes ANY owner-role membership on
    # an is_platform_admin account, so this second owner would silently
    # gain platform-admin too -- must fail, not merge.
    assert exits(
        seed_demo_owner.guard_account_slug,
        {"id": "a1", "owner_user_id": "demo-user"},
        [{"user_id": "someone-else"}],
        "demo-user",
    )

    # (a) issue #420 gap 1: owner_user_id points at a different user with NO
    # matching owner-role membership row at all (foreign_owners empty) --
    # must now be refused instead of silently adopted and overwritten.
    assert exits(
        seed_demo_owner.guard_account_slug,
        {"id": "a1", "owner_user_id": "someone-else-entirely"},
        [],
        "demo-user",
    )

    # password_to_set: an account that already exists keeps its password unless
    # the caller explicitly supplies one. Rotating it revokes every live session
    # for the shared demo account, which broke concurrent agents repeatedly.
    assert seed_demo_owner.password_to_set(True, "", "generated-pw") is None
    assert seed_demo_owner.password_to_set(True, "  ", "generated-pw") is None
    assert seed_demo_owner.password_to_set(True, "explicit-pw", "generated-pw") == "explicit-pw"
    assert seed_demo_owner.password_to_set(False, "explicit-pw", "generated-pw") == "explicit-pw"

    # A brand-new account has no session to break and no credential to keep, so
    # it still gets the freshly generated password the caller passed in.
    assert seed_demo_owner.password_to_set(False, "", "generated-pw") == "generated-pw"

    # random_password itself: length clears GoTrue minimums, prefix guarantees
    # all four character classes, and consecutive draws differ.
    generated = seed_demo_owner.random_password()
    assert len(generated) == 28 and generated.startswith("Aa1!")
    assert generated != seed_demo_owner.random_password()

    # env_or: identity overrides. An unset, empty, or whitespace-only variable
    # keeps the default, so an existing caller that sets nothing provisions the
    # same demo identity it always did.
    assert seed_demo_owner.env_or("HIVE_DEMO_TEST_UNSET_VAR", "fallback") == "fallback"
    for value in ("", "   "):
        os.environ["HIVE_DEMO_TEST_VAR"] = value
        assert seed_demo_owner.env_or("HIVE_DEMO_TEST_VAR", "fallback") == "fallback"
    os.environ["HIVE_DEMO_TEST_VAR"] = "  owner@example.test  "
    assert seed_demo_owner.env_or("HIVE_DEMO_TEST_VAR", "fallback") == "owner@example.test"
    del os.environ["HIVE_DEMO_TEST_VAR"]

    # And the constants themselves resolve through it, so a second owner is
    # provisionable without editing this script.
    assert seed_demo_owner.USER_EMAIL == "demo@hive-demo.invalid"
    assert seed_demo_owner.TENANT_SLUG == "hive-demo"
    assert seed_demo_owner.ACCOUNT_SLUG == "hive-demo-owner"

    print("ok: seed-demo-owner.py slug-collision guards + password_to_set + env_or")


if __name__ == "__main__":
    main()

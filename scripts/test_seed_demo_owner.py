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

    # validate_identity_overrides: the three identity variables must be set
    # together or not at all. A partial override (e.g. slugs set, email left
    # to its default) would otherwise silently attach the new tenant/account
    # to the SHARED default demo user instead of a separate owner.
    EMAIL, TSLUG, ASLUG = (
        "HIVE_DEMO_EMAIL",
        "HIVE_DEMO_TENANT_SLUG",
        "HIVE_DEMO_ACCOUNT_SLUG",
    )

    # None set: proceeds, the existing single-identity default is unchanged.
    assert not exits(seed_demo_owner.validate_identity_overrides, {})

    # All three set: proceeds, this is how a second, independent owner is
    # provisioned.
    assert not exits(
        seed_demo_owner.validate_identity_overrides,
        {EMAIL: "owner2@example.test", TSLUG: "second-tenant", ASLUG: "second-account"},
    )

    # Every partial combination (one or two of the three set) is refused.
    partial_combos = [
        {EMAIL: "owner2@example.test"},
        {TSLUG: "second-tenant"},
        {ASLUG: "second-account"},
        {EMAIL: "owner2@example.test", TSLUG: "second-tenant"},
        {EMAIL: "owner2@example.test", ASLUG: "second-account"},
        {TSLUG: "second-tenant", ASLUG: "second-account"},
    ]
    for combo in partial_combos:
        assert exits(seed_demo_owner.validate_identity_overrides, combo), combo

    # Whitespace-only counts as unset, matching env_or's own stripping: this
    # is still a partial override (email effectively missing), must refuse.
    assert exits(
        seed_demo_owner.validate_identity_overrides,
        {EMAIL: "   ", TSLUG: "second-tenant", ASLUG: "second-account"},
    )

    # ---- issue #1599: billing mapping and the explicit credit grant ----
    #
    # guard_billing_mapping: public.tenant_billing_accounts is 1:1 in both
    # directions (tenant_id is the primary key, account_id is UNIQUE), so a
    # single query for either side returns every row that can collide. Returns
    # True when the wanted pairing already exists (nothing to write), False
    # when there is nothing at all (write it), and exits on anything else --
    # repointing either side decides whose credits pay for whose traffic and is
    # an operator call, never a seeder's.
    T, A = "tenant-1", "account-1"

    # Nothing mapped: create it.
    assert seed_demo_owner.guard_billing_mapping([], T, A) is False

    # Exactly our pairing: a prior run of this script wrote it, idempotent.
    assert seed_demo_owner.guard_billing_mapping(
        [{"tenant_id": T, "account_id": A}], T, A
    ) is True

    # The tenant already bills to somebody else's account.
    assert exits(
        seed_demo_owner.guard_billing_mapping,
        [{"tenant_id": T, "account_id": "other-account"}],
        T,
        A,
    )

    # Our account already funds a different tenant.
    assert exits(
        seed_demo_owner.guard_billing_mapping,
        [{"tenant_id": "other-tenant", "account_id": A}],
        T,
        A,
    )

    # Both halves collide, with different partners on each side.
    assert exits(
        seed_demo_owner.guard_billing_mapping,
        [
            {"tenant_id": T, "account_id": "other-account"},
            {"tenant_id": "other-tenant", "account_id": A},
        ],
        T,
        A,
    )

    # credits_to_grant: credit is owner-discretionary. Unset means grant
    # nothing, so no path here ever funds a workspace implicitly; a value must
    # be an explicit positive integer of credits, and anything else is refused
    # loudly rather than rounded, truncated, or silently skipped.
    for unset in ("", "   ", None):
        assert seed_demo_owner.credits_to_grant(unset) is None
    assert seed_demo_owner.credits_to_grant("10000000000") == 10000000000
    assert seed_demo_owner.credits_to_grant("  10000000000  ") == 10000000000
    for bad in ("0", "-1", "abc", "12.5", "1e9", "10,000", "0x10"):
        assert exits(seed_demo_owner.credits_to_grant, bad), bad

    # Boundary: credits_delta is a bigint. The largest one is accepted and
    # anything past it is refused here, with the unit named, rather than
    # travelling to Postgres to come back as an out-of-range write error after
    # the rest of the workspace has already been provisioned.
    assert seed_demo_owner.credits_to_grant("9223372036854775807") == 9223372036854775807
    assert exits(seed_demo_owner.credits_to_grant, "9223372036854775808")

    # format_usd_from_credits: the confirmation line has to make a fat-fingered
    # unit visible, because the amount is credits and an operator thinking in
    # dollars is off by a billion. Integer arithmetic only: no float touches a
    # money quantity, even a displayed one.
    assert seed_demo_owner.format_usd_from_credits(0) == "$0.00"
    assert seed_demo_owner.format_usd_from_credits(10) == "$0.00"
    assert seed_demo_owner.format_usd_from_credits(1_000_000_000) == "$1.00"
    assert seed_demo_owner.format_usd_from_credits(10_000_000_000) == "$10.00"
    assert seed_demo_owner.format_usd_from_credits(1_234_560_000_000) == "$1234.56"
    # The fat-finger case itself: ten credits reads as nothing, ten dollars'
    # worth reads as ten dollars.
    assert seed_demo_owner.format_usd_from_credits(10) != seed_demo_owner.format_usd_from_credits(
        10_000_000_000
    )

    # grant_idempotency_key: keyed on the account AND the amount, so re-running
    # the seeder with the same amount cannot post a second grant (the unique
    # index on (account_id, entry_type, idempotency_key) swallows the replay),
    # while an operator deliberately topping up with a different amount can.
    assert seed_demo_owner.grant_idempotency_key(A, 5) == seed_demo_owner.grant_idempotency_key(A, 5)
    assert seed_demo_owner.grant_idempotency_key(A, 5) != seed_demo_owner.grant_idempotency_key(A, 6)
    assert seed_demo_owner.grant_idempotency_key(A, 5) != seed_demo_owner.grant_idempotency_key("a2", 5)

    # The ledger row itself: append-only, so it is inserted with
    # ignore-duplicates and never merged, it carries the positive delta as
    # written, and it states in its own metadata why the credit exists and what
    # granted it. A grant with no stated reason is not auditable after the fact.
    row = seed_demo_owner.grant_ledger_row(A, 7)
    assert row["account_id"] == A
    assert row["entry_type"] == "grant"
    assert row["credits_delta"] == 7
    assert row["idempotency_key"] == seed_demo_owner.grant_idempotency_key(A, 7)
    assert row["metadata"]["reason"]
    assert row["metadata"]["source"] == "scripts/seed-demo-owner.py"

    print(
        "ok: seed-demo-owner.py slug-collision guards + password_to_set + env_or + "
        "identity guard + billing mapping + explicit credit grant"
    )


if __name__ == "__main__":
    main()

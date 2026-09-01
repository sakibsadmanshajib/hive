#!/usr/bin/env python3
"""Self-check for scripts/shared_billing_mapping.py.

The tenant to billing account row decides whose credits pay for whose traffic,
and both seeders write it. It lived twice, once per seeder, until issue #1599:
`seed-owui-e2e-user.py` learned to write it in issue #717 and
`seed-demo-owner.py` did not, which is what made a freshly seeded demo owner
unable to use the product at all. One implementation, one self-check.

No framework, no network: the guard is pure, and the write path is driven
through a fake `request` callable that records what it was asked to do.
Run: python3 scripts/test_shared_billing_mapping.py
"""
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent))
import shared_billing_mapping  # noqa: E402

REST = "https://project.supabase.co/rest/v1"
HEADERS = {"Authorization": "Bearer service-role"}
TENANT = "11111111-1111-1111-1111-111111111111"
ACCOUNT = "22222222-2222-2222-2222-222222222222"
OTHER = "33333333-3333-3333-3333-333333333333"
OPTIONS = ("--tenant-slug", "--account-slug")


def exits(fn, *args, **kwargs) -> bool:
    try:
        fn(*args, **kwargs)
    except SystemExit as e:
        return e.code == 1
    return False


def recorder(answers):
    """A fake `request` returning canned answers in order, recording calls."""
    calls = []
    remaining = list(answers)

    def request(base, headers, method, path, body=None, params=None, prefer=None):
        calls.append((method, path, body, params))
        if not remaining:
            raise AssertionError(f"unexpected call: {method} {path}")
        return remaining.pop(0)

    return request, calls


def test_guard_is_pure_and_covers_both_directions() -> None:
    guard = shared_billing_mapping.guard_billing_mapping

    # Nothing mapped: write it.
    assert guard([], TENANT, ACCOUNT, OPTIONS) is False

    # Exactly the wanted pairing: a prior run wrote it, nothing to do.
    assert guard([{"tenant_id": TENANT, "account_id": ACCOUNT}], TENANT, ACCOUNT, OPTIONS) is True

    # The tenant already bills to another account.
    assert exits(guard, [{"tenant_id": TENANT, "account_id": OTHER}], TENANT, ACCOUNT, OPTIONS)

    # The account already funds another tenant.
    assert exits(guard, [{"tenant_id": OTHER, "account_id": ACCOUNT}], TENANT, ACCOUNT, OPTIONS)

    # Both halves collide, with a different partner on each side.
    assert exits(
        guard,
        [{"tenant_id": TENANT, "account_id": OTHER}, {"tenant_id": OTHER, "account_id": ACCOUNT}],
        TENANT,
        ACCOUNT,
        OPTIONS,
    )
    print("ok: guard_billing_mapping refuses to repoint either side")


def test_the_collision_message_names_the_caller_s_own_options() -> None:
    """Both seeders exit with the same rule and different remedies: one is
    driven by command line flags, the other by environment variables. A shared
    message naming the wrong one sends the operator to a knob that does not
    exist on the script they ran."""
    for options in (("--tenant-slug", "--account-slug"), ("HIVE_DEMO_TENANT_SLUG", "HIVE_DEMO_ACCOUNT_SLUG")):
        try:
            shared_billing_mapping.guard_billing_mapping(
                [{"tenant_id": TENANT, "account_id": OTHER}], TENANT, ACCOUNT, options
            )
        except SystemExit:
            pass
        else:
            raise AssertionError("expected a non-zero exit")
    print("ok: the collision message is parameterised by the caller's own options")


def test_write_when_absent() -> None:
    """The #717 and #1599 regression: without this row edge-api resolves no
    tenant for the caller, so it 403s account_not_provisioned on model listing
    and refuses chat with billing_not_configured."""
    request, calls = recorder([(200, []), (201, None)])
    shared_billing_mapping.provision_billing_mapping(request, REST, HEADERS, TENANT, ACCOUNT, OPTIONS)
    assert [c[0] for c in calls] == ["GET", "POST"], calls
    assert calls[1][1] == "/tenant_billing_accounts"
    assert calls[1][2] == {"tenant_id": TENANT, "account_id": ACCOUNT}
    print("ok: provision_billing_mapping writes the row when nothing is mapped")


def test_already_mapped_writes_nothing() -> None:
    request, calls = recorder([(200, [{"tenant_id": TENANT, "account_id": ACCOUNT}])])
    shared_billing_mapping.provision_billing_mapping(request, REST, HEADERS, TENANT, ACCOUNT, OPTIONS)
    assert [c[0] for c in calls] == ["GET"], calls
    print("ok: provision_billing_mapping is a no-op when the pairing already exists")


def test_lost_insert_race_on_the_same_pairing_is_success() -> None:
    """Two overlapping runs both read empty and both insert. The loser's
    conflict names a row that already says what it wanted, so failing on it
    would be a failure with nothing for anybody to act on."""
    request, calls = recorder([(200, []), (409, {"code": "23505"}), (200, [{"account_id": ACCOUNT}])])
    shared_billing_mapping.provision_billing_mapping(request, REST, HEADERS, TENANT, ACCOUNT, OPTIONS)
    assert [c[0] for c in calls] == ["GET", "POST", "GET"], calls
    print("ok: provision_billing_mapping accepts a lost insert race on the same pairing")


def test_lost_insert_race_to_a_different_pairing_fails() -> None:
    """Same race, but the winner mapped the tenant somewhere else (this is what
    signup.EnsureTenantBillingAccount doing its job concurrently looks like).
    Accepting that would silently hand the traffic to another account's
    credits."""
    request, _ = recorder([(200, []), (409, {"code": "23505"}), (200, [{"account_id": OTHER}])])
    assert exits(
        shared_billing_mapping.provision_billing_mapping,
        request,
        REST,
        HEADERS,
        TENANT,
        ACCOUNT,
        OPTIONS,
    )
    print("ok: provision_billing_mapping refuses a lost race to a different pairing")


def test_lookup_failure_is_fatal_not_a_write() -> None:
    """A failed read must never be mistaken for 'nothing is mapped'. That is
    the read that would repoint a live tenant."""
    request, calls = recorder([(500, None)])
    assert exits(
        shared_billing_mapping.provision_billing_mapping,
        request,
        REST,
        HEADERS,
        TENANT,
        ACCOUNT,
        OPTIONS,
    )
    assert [c[0] for c in calls] == ["GET"], calls
    print("ok: a failed lookup exits instead of writing")


def test_both_seeders_use_the_shared_implementation() -> None:
    """The point of the module. If a seeder grows its own copy again, this
    fails rather than waiting for the next issue #717."""
    for name in ("seed-demo-owner.py", "seed-owui-e2e-user.py"):
        source = (Path(__file__).parent / name).read_text()
        assert "shared_billing_mapping" in source, name
        assert "def provision_billing_mapping" not in source, name
    print("ok: both seeders route through the shared module")


def main() -> None:
    test_guard_is_pure_and_covers_both_directions()
    test_the_collision_message_names_the_caller_s_own_options()
    test_write_when_absent()
    test_already_mapped_writes_nothing()
    test_lost_insert_race_on_the_same_pairing_is_success()
    test_lost_insert_race_to_a_different_pairing_fails()
    test_lookup_failure_is_fatal_not_a_write()
    test_both_seeders_use_the_shared_implementation()
    print("ok: shared_billing_mapping")


if __name__ == "__main__":
    main()

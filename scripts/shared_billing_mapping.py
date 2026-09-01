"""The tenant to billing account mapping, for the seeders that provision one.

`public.tenant_billing_accounts` is the row that decides whose credits pay for
whose traffic. Edge-api resolves an API key's (and a chat caller's) tenant
through it, and `ledger.ResolveAccountIDForEmail` joins it to answer a balance,
so a tenant without one is provisioned and unusable at the same time: 403
`account_not_provisioned` on model listing, `billing_not_configured` on chat,
and no balance at all.

This lived twice, once per seeder, and the two copies drifted exactly as you
would expect. `seed-owui-e2e-user.py` learned to write the row in issue #717,
after a shim key that could not list a single model; `seed-demo-owner.py` did
not, and issue #1599 is the same defect reported again eighteen months of
commits later, this time as a demo owner who could sign in and not chat. One
implementation now, so a third seeder cannot be born without it.

`scripts/` is `sys.path[0]` for anything run as `python3 scripts/<name>.py`, so
a plain `import shared_billing_mapping` works from either seeder with no
packaging involved, including from `.github/workflows/demo-chat-settings-check.yml`,
which invokes the seeder by relative path from another directory. The filename
uses underscores for that reason; most scripts here are hyphenated and
therefore not importable. Same mechanism as `scripts/shared_demo_account.py`.

The caller passes its own `request` callable rather than this module owning
one, because each seeder sends its own User-Agent (Cloudflare answers 403 to
urllib's default on this deployment) and its own timeout, and neither belongs
to this rule.
"""

import sys


def guard_billing_mapping(rows, tenant_id: str, account_id: str, options) -> bool:
    """Decide what to do about existing `tenant_billing_accounts` rows.

    Returns True when the wanted pairing already exists (nothing to write) and
    False when nothing is mapped yet (write it). Exits on anything else.

    `rows` is every row matching EITHER side of the wanted pairing. That is the
    whole collision surface: the table is 1:1 in both directions (`tenant_id`
    is the primary key, `account_id` is UNIQUE), which is what keeps one credit
    balance from funding two tenants. Repointing either side is an operator
    decision about whose credits pay for whose traffic, never a seeder's, so a
    collision exits before anything is written or minted.

    `options` is the (tenant_option, account_option) pair naming the knobs the
    CALLING script exposes, so the exit message sends the operator to a flag or
    a variable that actually exists on the script they ran.
    """
    if not rows:
        return False
    if len(rows) == 1 and rows[0]["tenant_id"] == tenant_id and rows[0]["account_id"] == account_id:
        return True

    tenant_option, account_option = options
    for row in rows:
        if row["tenant_id"] == tenant_id:
            print(
                f"error: tenant {tenant_id} already bills to account {row['account_id']}, not "
                f"{account_id}. One tenant bills to exactly one account. Point this run at that "
                f"account with {account_option}, or give this identity its own tenant with "
                f"{tenant_option}.",
                file=sys.stderr,
            )
        if row["account_id"] == account_id:
            print(
                f"error: account {account_id} already funds tenant {row['tenant_id']}, not "
                f"{tenant_id}. An account funds at most one tenant. Point this run at that tenant "
                f"with {tenant_option}, or use a different account with {account_option}.",
                file=sys.stderr,
            )
    sys.exit(1)


def provision_billing_mapping(request, rest, headers, tenant_id: str, account_id: str, options) -> None:
    """Map `tenant_id` to `account_id`, idempotently, or exit non-zero.

    Shape follows the live signup path, `signup.EnsureTenantBillingAccount`:
    one row, tenant plus account, no ids invented here. The pairing is asserted
    rather than derived, because that path's convergence predicate cannot
    resolve a seeded fixture (a bootstrap member with no billing account of its
    own reads as "not converged yet" forever).

    A single `or=` query is the complete collision surface, given the 1:1
    constraints, so one read answers both directions.
    """
    status, rows = request(
        rest, headers, "GET", "/tenant_billing_accounts",
        params={
            "or": f"(tenant_id.eq.{tenant_id},account_id.eq.{account_id})",
            "select": "tenant_id,account_id",
        },
    )
    if status != 200 or rows is None:
        # A failed read must never be mistaken for "nothing is mapped": that is
        # the read that would repoint a live tenant.
        print(f"error: billing mapping lookup failed: {status} {rows}", file=sys.stderr)
        sys.exit(1)

    if guard_billing_mapping(rows, tenant_id, account_id, options):
        print("billing mapping: ok (already mapped)", file=sys.stderr)
        return

    status, body = request(
        rest, headers, "POST", "/tenant_billing_accounts",
        body={"tenant_id": tenant_id, "account_id": account_id},
    )
    if status in (200, 201, 204):
        print("billing mapping: ok (created)", file=sys.stderr)
        return

    # A concurrent run can pass the guard above and insert first, so losing
    # either uniqueness constraint to a row that says exactly what this run
    # wanted is a success, not a failure. Anything else, including the winner
    # having mapped this tenant somewhere else, is the collision the guard
    # describes and stays fatal.
    reread_status, reread = request(
        rest, headers, "GET", "/tenant_billing_accounts",
        params={"tenant_id": f"eq.{tenant_id}", "select": "account_id"},
    )
    if reread_status == 200 and reread and reread[0]["account_id"] == account_id:
        print("billing mapping: ok (a concurrent run created the same pairing first)", file=sys.stderr)
        return
    print(f"error: billing mapping insert failed: {status} {body}", file=sys.stderr)
    sys.exit(1)

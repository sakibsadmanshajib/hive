"""The shared-account rule, for the Python scripts that mint live sessions.

`apps/web-console/tests/e2e/support/live-auth.mjs` is the single door for
JavaScript runs; this module is the same rule for the three Python scripts
that reach a deployed environment on their own:

  * `verify-control-plane.py`, which signs in and then mints a real API key and
    sends a real completion through it.
  * `post-deploy-verify.py`, whose `signin` and `ledger` checks sign in (the
    `ledger` check spends a real completion too).
  * `verify-rag-roundtrip.py`, which mints through GoTrue's admin one-time-token
    flow. Its address is a module-level literal, dedicated to that script and
    already outside `PROTECTED_ACCOUNT_BASES` below, so it is unaffected here.

`scripts/` is `sys.path[0]` for anything run as `python3 scripts/<name>.py`, so
a plain import works and no packaging is involved. The filename uses
underscores for that reason; most scripts here are hyphenated and therefore not
importable.

Issue #1462 made this a denylist of exactly one address
(`demo@hive-demo.invalid`). Issue #1476 replaces it with a run-key allowlist:
minting for a protected base now requires either `E2E_RUN_KEY` (an environment
variable, read here directly, never an argument a caller could fake) to appear
in the address, or an explicit `read_only=True` at the call site, the same
escape `assertNotSharedDemoAccount` in live-auth.mjs already used for the demo
account alone.

`PROTECTED_ACCOUNT_BASES` names the three distinct base addresses behind
issue #1476's four-row table (`e2e-verified+qafunded-...@scubed.com.bd` is a
`+`-tagged instance of the `e2e-verified@scubed.com.bd` base, not a fourth
base). This module still hardcodes those three, which is short of the issue's
stated end state of zero hardcoded addresses: `verify-control-plane.py` and
`post-deploy-verify.py` mint through this same guard for identities that are
operator-declared (`HIVE_VERIFY_EMAIL`, no default) or run-key-free by design
(`post-deploy-verify.py`'s `ledger` check spends real money on every
`deploy-demo-box` completion, autonomously, with no `E2E_RUN_KEY` set in that
workflow), and a literal-free "every address needs a run key" rule would have
refused both, trading a known incident for a new one on the deploy path. Scoping
the requirement to the three known-litter-prone bases keeps every other caller's
behaviour exactly as it was.
"""

import os

PROTECTED_ACCOUNT_BASES = frozenset(
    {
        "demo@hive-demo.invalid",
        "qa-tester@hive.test",
        "e2e-verified@scubed.com.bd",
    }
)


def _normalise(email: str | None) -> str:
    return (email or "").strip().lower()


def _base(email: str) -> str:
    """`local+tag@domain` -> `local@domain`. GoTrue does not fold the tag away
    itself, so a stale or hardcoded tag on a protected base is still that base;
    this is what lets the run-key check below see through it.
    """
    local, sep, domain = email.partition("@")
    if not sep:
        return email
    return f"{local.split('+', 1)[0]}@{domain}"


def is_shared_demo_account(email: str | None) -> bool:
    """True when `email`'s base (see `_base`) is one of `PROTECTED_ACCOUNT_BASES`.

    Normalised the same way live-auth.mjs normalises: trimmed, lowercased.
    `Demo@hive-demo.invalid`, or the address with a trailing space, is the same
    account. GoTrue folds case on the local part for lookup, so an exact,
    case-sensitive `==` is a guard that the account's own login flow walks past.

    True for a `+`-tagged address on a protected base too, run-key-scoped or
    not: this function answers "is it one of the protected bases", not "is it
    allowed through". `assert_not_shared_demo_account` below is where the
    run-key and read-only escapes live.
    """
    normalised = _normalise(email)
    if not normalised:
        return False
    return _base(normalised) in PROTECTED_ACCOUNT_BASES


def assert_not_shared_demo_account(
    email: str | None, *, variable: str, doing: str, read_only: bool = False
) -> None:
    """Raises SystemExit if `email` is a protected base and neither escape applies.

    `variable` names the environment variable the caller read, and `doing`
    describes the write this run would perform, so the refusal says what would
    have landed rather than only that something was refused.

    Escapes, either of which lets a protected-base address through:
      * `read_only=True`, for a call site that only signs in and reads.
      * `E2E_RUN_KEY` set in the environment and present in `email`, for a
        run-scoped identity derived from one of these bases.
    """
    if not is_shared_demo_account(email):
        return
    if read_only:
        return
    run_key = os.environ.get("E2E_RUN_KEY", "").strip().lower()
    if run_key and run_key in _normalise(email):
        return
    raise SystemExit(
        f"error: {variable} is {_normalise(email)}, one of the shared accounts "
        "that accumulates automation litter when reused outside its own run "
        f"(issues #848, #916, #1476). This run {doing}. Use a dedicated, "
        "E2E_RUN_KEY-scoped identity instead, or pass read_only=True if this "
        "call only signs in and reads. See docs/live-test-auth.md."
    )

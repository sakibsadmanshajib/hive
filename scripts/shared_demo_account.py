"""The demo-account rule, for the Python scripts that mint live sessions.

`apps/web-console/tests/e2e/support/live-auth.mjs` is the single door for
JavaScript runs and refuses `demo@hive-demo.invalid` there. It was not the
single door for the repository: three Python scripts reach a deployed
environment on their own, and until this module the rule was enforced in one of
them, by a bare `==` against an exact string with no case folding and no trim,
while the other two carried it only as prose in a docstring. A rule stated in
prose and enforced by nothing is what issue #848 is about, so having two
implementations of it that disagree on normalisation was the same defect in a
smaller box.

One implementation, imported by every Python caller that can authenticate:

  * `verify-control-plane.py`, which signs in and then mints a real API key and
    sends a real completion through it.
  * `post-deploy-verify.py`, whose `signin` and `ledger` checks sign in.
  * `verify-rag-roundtrip.py`, which mints through GoTrue's admin one-time-token
    flow. Its address is a module-level literal, so it was already safe, but by
    accident of that literal rather than by a check.

`scripts/` is `sys.path[0]` for anything run as `python3 scripts/<name>.py`, so
a plain import works and no packaging is involved. The filename uses
underscores for that reason; most scripts here are hyphenated and therefore not
importable.

Deliberately NOT an allowlist. Requiring every address to carry `E2E_RUN_KEY`
would cover the three other shared identities that accumulate the same litter,
and it is the right end state, but two scheduled workflows authenticate as
persistent identities with no run key today (`demo-chat-settings-check.yml`
through `HIVE_QA_TESTER_EMAIL`, and `owui-nightly.yml`, which sets
`OWUI_E2E_RUN_KEY` rather than `E2E_RUN_KEY`). Turning those red without first
provisioning run-key-scoped identities for them would trade one silent problem
for a loud unrelated one. Tracked as issue #1476.
"""

SHARED_DEMO_ACCOUNT = "demo@hive-demo.invalid"


def is_shared_demo_account(email: str | None) -> bool:
    """Normalised the same way live-auth.mjs normalises: trimmed, lowercased.

    `Demo@hive-demo.invalid`, or the address with a trailing space, is the same
    account. GoTrue folds case on the local part for lookup, so an exact,
    case-sensitive `==` is a guard that the account's own login flow walks past.
    """
    return (email or "").strip().lower() == SHARED_DEMO_ACCOUNT


def assert_not_shared_demo_account(email: str | None, *, variable: str, doing: str) -> None:
    """Raises SystemExit if `email` is the shared demo account.

    `variable` names the environment variable the caller read, and `doing`
    describes the write this run would perform, so the refusal says what would
    have landed on the surface the owner shows to prospects rather than only
    that something was refused.
    """
    if not is_shared_demo_account(email):
        return
    raise SystemExit(
        f"error: {variable} is {SHARED_DEMO_ACCOUNT}, the shared account the owner "
        f"demos to prospects. This run {doing}, and issue #848 exists because that "
        "traffic ended up on that account's own sidebar. Use a dedicated, "
        "E2E_RUN_KEY-scoped identity instead. See docs/live-test-auth.md."
    )

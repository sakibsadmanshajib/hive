#!/usr/bin/env python3
"""Self-check for the Python half of the demo-account rule (issues #848, #916).

Two things are asserted, and the second matters more than the first.

  1. The normalisation. `demo@hive-demo.invalid` written with a stray capital
     or a trailing space is the same account, and the exact-string comparison
     this replaced in post-deploy-verify.py let both walk past.
  2. The wiring. A guard nothing calls is the defect this repository keeps
     shipping, so each of the three Python scripts that can authenticate
     against a deployed environment is checked for an actual call, not for a
     sentence about one in its docstring. verify-control-plane.py had exactly
     such a sentence and no check, which is what put this module here.

Structural, no framework, no network.
Run: python3 scripts/test_shared_demo_account.py
"""

import ast
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

from shared_demo_account import (  # noqa: E402
    SHARED_DEMO_ACCOUNT,
    assert_not_shared_demo_account,
    is_shared_demo_account,
)

REPO_ROOT = Path(__file__).resolve().parents[1]
SCRIPTS = REPO_ROOT / "scripts"

# Every Python script that mints or signs in against a deployed environment.
# Adding one without a call here should be caught by review; adding one and
# forgetting the guard is what this list makes loud.
GUARDED_SCRIPTS = (
    "verify-control-plane.py",
    "post-deploy-verify.py",
    "verify-rag-roundtrip.py",
)

failures: list[str] = []


def check(condition: bool, message: str) -> None:
    if condition:
        print(f"  ok: {message}")
    else:
        failures.append(message)
        print(f"  FAIL: {message}")


def refused(email: str | None) -> bool:
    try:
        assert_not_shared_demo_account(email, variable="X", doing="writes")
    except SystemExit:
        return True
    return False


def calls_guard(path: Path) -> bool:
    """A real call to assert_not_shared_demo_account, not a mention of it."""
    tree = ast.parse(path.read_text(encoding="utf-8"))
    imported = any(
        isinstance(node, ast.ImportFrom)
        and node.module == "shared_demo_account"
        and any(a.name == "assert_not_shared_demo_account" for a in node.names)
        for node in ast.walk(tree)
    )
    called = any(
        isinstance(node, ast.Call)
        and isinstance(node.func, ast.Name)
        and node.func.id == "assert_not_shared_demo_account"
        for node in ast.walk(tree)
    )
    return imported and called


def main() -> int:
    print("shared demo-account rule (issues #848, #916)")

    print("\nnormalisation")
    check(is_shared_demo_account(SHARED_DEMO_ACCOUNT), "the plain address is recognised")
    check(
        is_shared_demo_account(f"  {SHARED_DEMO_ACCOUNT.upper()} "),
        "a stray capital and surrounding whitespace are the same account",
    )
    check(
        not is_shared_demo_account("e2e-verified+run-key-1@scubed.com.bd"),
        "a run-key-scoped identity is not the demo account",
    )
    check(
        not is_shared_demo_account("owner@hive-demo.invalid"),
        "another address on the same domain is not the demo account",
    )
    check(not is_shared_demo_account(""), "an empty address is not the demo account")
    check(not is_shared_demo_account(None), "a missing address is not the demo account")

    print("\nrefusal")
    check(refused(SHARED_DEMO_ACCOUNT), "the demo account is refused")
    check(
        refused(f" {SHARED_DEMO_ACCOUNT.title()} "),
        "the demo account is refused however it is capitalised or padded",
    )
    check(
        not refused("e2e-verified+run-key-1@scubed.com.bd"),
        "a run-key-scoped identity is allowed through",
    )
    message = ""
    try:
        assert_not_shared_demo_account(
            SHARED_DEMO_ACCOUNT, variable="HIVE_VERIFY_EMAIL", doing="mints a real API key"
        )
    except SystemExit as exit_error:
        message = str(exit_error)
    check("HIVE_VERIFY_EMAIL" in message, "the refusal names the variable the caller read")
    check("mints a real API key" in message, "the refusal names the write that would land")
    check("docs/live-test-auth.md" in message, "the refusal points at the document")

    print("\nwiring")
    for name in GUARDED_SCRIPTS:
        path = SCRIPTS / name
        check(path.exists(), f"{name} exists")
        if path.exists():
            check(calls_guard(path), f"{name} imports and calls the guard, not just mentions it")

    print()
    if failures:
        print(f"FAILED: {len(failures)} check(s)")
        for f in failures:
            print(f"  - {f}")
        return 1
    print("PASS")
    return 0


if __name__ == "__main__":
    sys.exit(main())

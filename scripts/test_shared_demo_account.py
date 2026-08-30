#!/usr/bin/env python3
"""Self-check for the Python half of the shared-account rule (issues #848,
#916, #1476).

Three things are asserted, and the third matters most.

  1. The normalisation. `demo@hive-demo.invalid` written with a stray capital
     or a trailing space is the same account, and the exact-string comparison
     this replaced in post-deploy-verify.py let both walk past.
  2. The allowlist. A protected base is refused unless the caller declares
     `read_only=True` or the environment's `E2E_RUN_KEY` appears in the
     address; a run-key-scoped identity on the same base is let through; an
     address that is not one of the protected bases at all is never touched by
     either escape, because it never needed one.
  3. The wiring. A guard nothing calls is the defect this repository keeps
     shipping, so each of the three Python scripts that can authenticate
     against a deployed environment is checked for an actual call, not for a
     sentence about one in its docstring. verify-control-plane.py had exactly
     such a sentence and no check, which is what put this module here.

Structural, no framework, no network.
Run: python3 scripts/test_shared_demo_account.py
"""

import ast
import os
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

from shared_demo_account import (  # noqa: E402
    PROTECTED_ACCOUNT_BASES,
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


def with_run_key(value: str | None, fn):
    """Runs `fn` with E2E_RUN_KEY set to `value` (or unset when `value` is
    None), restoring whatever was there before, even on failure.
    """
    previous = os.environ.get("E2E_RUN_KEY")
    try:
        if value is None:
            os.environ.pop("E2E_RUN_KEY", None)
        else:
            os.environ["E2E_RUN_KEY"] = value
        return fn()
    finally:
        if previous is None:
            os.environ.pop("E2E_RUN_KEY", None)
        else:
            os.environ["E2E_RUN_KEY"] = previous


def refused(email: str | None, *, read_only: bool = False) -> bool:
    try:
        assert_not_shared_demo_account(
            email, variable="X", doing="writes", read_only=read_only
        )
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
    print("shared-account rule (issues #848, #916, #1476)")

    print("\nnormalisation")
    for base in sorted(PROTECTED_ACCOUNT_BASES):
        check(is_shared_demo_account(base), f"{base} is recognised")
        check(
            is_shared_demo_account(f"  {base.upper()} "),
            f"a stray capital and surrounding whitespace on {base} are the same account",
        )
    check(
        not is_shared_demo_account("rag-verify-e2e@hive-e2e.invalid"),
        "a dedicated, unrelated address is not a protected base",
    )
    check(not is_shared_demo_account(""), "an empty address is not a protected base")
    check(not is_shared_demo_account(None), "a missing address is not a protected base")

    print("\nrun-key allowlist")
    demo = "demo@hive-demo.invalid"
    check(
        with_run_key(None, lambda: refused(demo)),
        "a protected base is refused with no run key and no read_only",
    )
    check(
        with_run_key("", lambda: refused(demo)),
        "an empty E2E_RUN_KEY does not silently allow a protected base through",
    )
    check(
        with_run_key("run-42", lambda: not refused(f"e2e-verified+run-42@scubed.com.bd")),
        "a run-key-scoped identity on a protected base is allowed through",
    )
    check(
        with_run_key("run-42", lambda: refused("e2e-verified+run-99@scubed.com.bd")),
        "a different run's tag on the same base is still refused",
    )
    check(
        with_run_key(None, lambda: not refused(demo, read_only=True)),
        "read_only=True allows a protected base through with no run key",
    )
    check(
        with_run_key("anything", lambda: not refused("rag-verify-e2e@hive-e2e.invalid")),
        "an address that is not a protected base needs neither escape",
    )

    print("\nrefusal message")
    message = ""
    try:
        with_run_key(
            None,
            lambda: assert_not_shared_demo_account(
                demo, variable="HIVE_VERIFY_EMAIL", doing="mints a real API key"
            ),
        )
    except SystemExit as exit_error:
        message = str(exit_error)
    check("HIVE_VERIFY_EMAIL" in message, "the refusal names the variable the caller read")
    check("mints a real API key" in message, "the refusal names the write that would land")
    check("docs/live-test-auth.md" in message, "the refusal points at the document")
    check("#1476" in message, "the refusal cites this issue")

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

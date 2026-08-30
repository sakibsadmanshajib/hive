#!/usr/bin/env python3
"""Self-check for the Python half of the shared-account rule (issues #848,
#916, #1476).

Four things are asserted, and the last two matter most.

  1. The normalisation. `demo@hive-demo.invalid` written with a stray capital
     or a trailing space is the same account, and the exact-string comparison
     this replaced in post-deploy-verify.py let both walk past. Also asserts
     that a leading U+FEFF and a trailing U+001C are stripped here exactly as
     live-auth.mjs strips them, since a disagreement there let the same
     address be refused by one entry point and allowed by the other.
  2. The allowlist. A protected base is refused unless the caller declares
     `read_only=True` or the environment's `E2E_RUN_KEY` is at least
     `MIN_RUN_KEY_LENGTH` characters and matches the `+tag` on the address's
     local part exactly; an address that is not one of the protected bases at
     all is never touched by either escape, because it never needed one.
  3. The bypass table. An earlier version of this check tested `runKey in
     normalisedAddress` (a substring test against the *whole* address,
     domain included), which a reviewer found lets a short, common key open a
     protected base with its address completely unchanged: `E2E_RUN_KEY=-`
     opened all three bases; `hive` opened two; `qa`/`test` opened
     `qa-tester@hive.test`; `e2e`/`2`/`bd` opened `e2e-verified@scubed.com.bd`;
     `invalid`/`demo` opened `demo@hive-demo.invalid`. Every one of those keys
     is asserted refused here, against every base, with no tag present at all.
  4. The wiring. A guard nothing calls is the defect this repository keeps
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
    MIN_RUN_KEY_LENGTH,
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
    check(
        is_shared_demo_account("﻿demo@hive-demo.invalid"),
        "a leading U+FEFF is stripped the same as any other edge character "
        "(JS's trim() folds this in, so both sides must agree)",
    )
    check(
        is_shared_demo_account("qa-tester@hive.test\x1c"),
        "a trailing U+001C is stripped the same as any other edge character "
        "(Python's strip() folds this in, so both sides must agree)",
    )

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
        with_run_key("run-4242", lambda: not refused("e2e-verified+run-4242@scubed.com.bd")),
        "a run-key-scoped identity on a protected base is allowed through",
    )
    check(
        with_run_key("run-4242", lambda: refused("e2e-verified+run-9999@scubed.com.bd")),
        "a different run's tag on the same base is still refused",
    )
    check(
        with_run_key(None, lambda: not refused(demo, read_only=True)),
        "read_only=True allows a protected base through with no run key",
    )
    check(
        with_run_key("anything!", lambda: not refused("rag-verify-e2e@hive-e2e.invalid")),
        "an address that is not a protected base needs neither escape",
    )
    check(
        with_run_key("run-4242", lambda: refused("e2e-verified@scubed.com.bd")),
        "the bare base is still refused even when a run key is set (no tag at all)",
    )
    check(
        with_run_key("run", lambda: refused("e2e-verified+run@scubed.com.bd")),
        f"a run key shorter than MIN_RUN_KEY_LENGTH ({MIN_RUN_KEY_LENGTH}) is refused even as an exact tag match",
    )
    check(
        with_run_key(
            "scubedotcombd",
            # A key that looks domain-derived but is not the actual tag
            # ("notrun"). Exact-tag comparison must still refuse this,
            # proving the domain never participates in the match.
            lambda: refused("e2e-verified+notrun@scubed.com.bd"),
        ),
        "a run key that only resembles the domain, not the local-part tag, is refused",
    )

    print("\nbypass table (substring-of-whole-address regression, not just the tag)")
    # Each of these keys is a substring of the bare address for at least one
    # protected base. A substring-of-the-whole-address check (the bug this
    # guards against) would let the matching base(s) through with the address
    # completely unchanged, no +tag at all. Every one of these must still be
    # refused: the run-key check only ever looks at an exact +tag match.
    bypass_keys = ("-", "hive", "qa", "test", "e2e", "2", "bd", "invalid", "demo")
    for key in bypass_keys:
        for base in sorted(PROTECTED_ACCOUNT_BASES):
            check(
                with_run_key(key, lambda base=base: refused(base)),
                f"E2E_RUN_KEY={key!r} does not open {base} (bare address, no tag)",
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

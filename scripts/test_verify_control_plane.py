#!/usr/bin/env python3
"""Self-check for the HIVE_VERIFY_EMAIL guard in verify-control-plane.py (#848).

This script mints a real API key and sends a real POST /v1/chat/completions
through it, so it must never default its caller identity to the shared demo
account. No framework, no network: asserts the module-level default is empty
and that main() exits before any HTTP call when the identity is unset.
Run: python3 scripts/test_verify_control_plane.py
"""
import importlib.util
import os
from pathlib import Path

spec = importlib.util.spec_from_file_location(
    "verify_control_plane", Path(__file__).parent / "verify-control-plane.py"
)
verify_control_plane = importlib.util.module_from_spec(spec)
# EMAIL resolves at import time; drop any override so this asserts the
# script's actual default rather than whatever the caller's shell exports.
os.environ.pop("HIVE_VERIFY_EMAIL", None)
spec.loader.exec_module(verify_control_plane)


def test_the_default_identity_is_never_the_demo_account() -> None:
    assert verify_control_plane.EMAIL == "", verify_control_plane.EMAIL


def test_main_exits_before_any_request_when_email_is_unset() -> None:
    # The other four required variables are supplied so the failure under
    # test is unambiguously the email check, not an earlier one.
    env = dict(os.environ)
    env.update(
        SUPABASE_URL="https://example.invalid",
        SUPABASE_ANON_KEY="dummy",
        CONTROL_PLANE_INTERNAL_TOKEN="dummy",
        HIVE_VERIFY_PASSWORD="dummy",
    )
    env.pop("HIVE_VERIFY_EMAIL", None)
    os.environ.clear()
    os.environ.update(env)

    def exploding_http(*_args, **_kwargs):
        raise AssertionError("main() attempted a request before checking HIVE_VERIFY_EMAIL")

    original_http = verify_control_plane.http
    verify_control_plane.http = exploding_http
    try:
        try:
            verify_control_plane.main()
            raise AssertionError("main() did not exit with HIVE_VERIFY_EMAIL unset")
        except SystemExit as e:
            assert "HIVE_VERIFY_EMAIL" in str(e), e
    finally:
        verify_control_plane.http = original_http


def main() -> None:
    for name, fn in sorted(globals().items()):
        if name.startswith("test_") and callable(fn):
            fn()
    print("ok: verify-control-plane HIVE_VERIFY_EMAIL guard (issue #848)")


if __name__ == "__main__":
    main()

#!/usr/bin/env python3
"""Self-check for the OWUI config sync in seed-owui-e2e-user.py (fix for
the live incident where OWUI's own persisted OpenAI config drifts from a
freshly rotated SHIM_KEY -- see PR #423 body). No framework, no network:
mocks urllib.request.urlopen and exercises merge_owui_config and
sync_owui_config directly. Run: python3 scripts/test_seed_owui_e2e_user.py
"""
import importlib.util
import io
import json
import os
import sys
import urllib.error
import urllib.request
from pathlib import Path

spec = importlib.util.spec_from_file_location(
    "seed_owui_e2e_user", Path(__file__).parent / "seed-owui-e2e-user.py"
)
seed_owui_e2e_user = importlib.util.module_from_spec(spec)
spec.loader.exec_module(seed_owui_e2e_user)


class FakeResponse:
    def __init__(self, status, body):
        self.status = status
        self._raw = json.dumps(body).encode()

    def read(self):
        return self._raw

    def __enter__(self):
        return self

    def __exit__(self, *exc):
        return False


def patch_urlopen(fn):
    original = urllib.request.urlopen
    urllib.request.urlopen = fn
    return original


def restore_urlopen(original) -> None:
    urllib.request.urlopen = original


# --- merge_owui_config: pure function tests -------------------------------

def test_merge_appends_when_no_existing_entry() -> None:
    merged = seed_owui_e2e_user.merge_owui_config({}, "http://edge-api:8080/v1", "hk_new")
    assert merged["OPENAI_API_BASE_URLS"] == ["http://edge-api:8080/v1"]
    assert merged["OPENAI_API_KEYS"] == ["hk_new"]
    assert merged["OPENAI_API_CONFIGS"] == {"0": {"enable": True}}
    print("ok: merge_owui_config appends when there is no existing entry")


def test_merge_preserves_other_entries_and_updates_matching_one() -> None:
    existing = {
        "ENABLE_OPENAI_API": True,
        "OPENAI_API_BASE_URLS": ["https://api.openai.com/v1", "http://edge-api:8080/v1"],
        "OPENAI_API_KEYS": ["sk-someone-elses-key", "hk_old_dead_key"],
        "OPENAI_API_CONFIGS": {"0": {"enable": True}, "1": {"enable": True}},
    }
    merged = seed_owui_e2e_user.merge_owui_config(existing, "http://edge-api:8080/v1", "hk_new")
    # the OTHER connection (index 0) must survive untouched
    assert merged["OPENAI_API_BASE_URLS"][0] == "https://api.openai.com/v1"
    assert merged["OPENAI_API_KEYS"][0] == "sk-someone-elses-key"
    assert merged["OPENAI_API_CONFIGS"]["0"] == {"enable": True}
    # our own connection (index 1) is updated in place, not appended again
    assert merged["OPENAI_API_BASE_URLS"] == ["https://api.openai.com/v1", "http://edge-api:8080/v1"]
    assert merged["OPENAI_API_KEYS"][1] == "hk_new"
    print("ok: merge_owui_config preserves other connections and updates only its own")


# --- sync_owui_config: full flow tests ------------------------------------

def test_sync_full_flow_preserves_other_entries_in_final_post() -> None:
    """The end-to-end regression the P1 review thread demanded: mock a
    multi-entry existing config and assert the final POST body still
    carries the other entry untouched."""
    calls = []
    existing_config = {
        "OPENAI_API_BASE_URLS": ["https://api.openai.com/v1"],
        "OPENAI_API_KEYS": ["sk-someone-elses-key"],
        "OPENAI_API_CONFIGS": {"0": {"enable": True}},
    }

    def fake_urlopen(req, timeout=None):
        calls.append(req)
        if req.full_url.endswith("/api/v1/auths/signin"):
            return FakeResponse(200, {"token": "fake-jwt"})
        if req.full_url.endswith("/openai/config") and req.get_method() == "GET":
            return FakeResponse(200, existing_config)
        if req.full_url.endswith("/openai/config/update"):
            return FakeResponse(200, {"status": True})
        raise AssertionError(f"unexpected call: {req.get_method()} {req.full_url}")

    original = patch_urlopen(fake_urlopen)
    try:
        seed_owui_e2e_user.sync_owui_config("hk_new")
    finally:
        restore_urlopen(original)

    assert len(calls) == 3
    posted = json.loads(calls[2].data)
    assert posted["OPENAI_API_BASE_URLS"] == ["https://api.openai.com/v1", "http://edge-api:8080/v1"]
    assert posted["OPENAI_API_KEYS"] == ["sk-someone-elses-key", "hk_new"]
    assert calls[2].headers.get("Authorization") == "Bearer fake-jwt"
    print("ok: sync_owui_config's final POST preserves the other pre-existing connection")


def test_sync_never_raises_on_unreachable_owui() -> None:
    def fake_urlopen(req, timeout=None):
        raise urllib.error.URLError("connection refused")

    original = patch_urlopen(fake_urlopen)
    try:
        seed_owui_e2e_user.sync_owui_config("hk_secret123")  # must not raise
    finally:
        restore_urlopen(original)
    print("ok: sync_owui_config does not raise when OWUI is unreachable")


def test_sync_signin_fails_no_token() -> None:
    """A valid-JSON-but-non-dict signin response must not raise
    AttributeError before the never-fatal contract kicks in -- P2 nit:
    isinstance-guard body.get("token")."""
    def fake_urlopen(req, timeout=None):
        return FakeResponse(200, ["not", "a", "dict"])

    original = patch_urlopen(fake_urlopen)
    try:
        seed_owui_e2e_user.sync_owui_config("hk_secret123")  # must not raise
    finally:
        restore_urlopen(original)
    print("ok: sync_owui_config does not raise when signin body is not a dict")


def test_sync_config_update_non_200() -> None:
    def fake_urlopen(req, timeout=None):
        if req.full_url.endswith("/api/v1/auths/signin"):
            return FakeResponse(200, {"token": "fake-jwt"})
        if req.full_url.endswith("/openai/config") and req.get_method() == "GET":
            return FakeResponse(200, {})
        if req.full_url.endswith("/openai/config/update"):
            return FakeResponse(500, {"detail": "boom"})
        raise AssertionError(f"unexpected call: {req.full_url}")

    original = patch_urlopen(fake_urlopen)
    try:
        seed_owui_e2e_user.sync_owui_config("hk_secret123")  # must not raise
    finally:
        restore_urlopen(original)
    print("ok: sync_owui_config does not raise on a non-200 config update")


def test_sync_skips_without_admin_credentials() -> None:
    calls = []

    def fake_urlopen(req, timeout=None):
        calls.append(req)
        raise AssertionError("must not call network without admin credentials")

    original = patch_urlopen(fake_urlopen)
    old_email = os.environ.pop("OWUI_ADMIN_EMAIL", None)
    old_password = os.environ.pop("OWUI_ADMIN_PASSWORD", None)
    try:
        seed_owui_e2e_user.sync_owui_config("hk_secret123")
    finally:
        restore_urlopen(original)
        if old_email is not None:
            os.environ["OWUI_ADMIN_EMAIL"] = old_email
        if old_password is not None:
            os.environ["OWUI_ADMIN_PASSWORD"] = old_password

    assert calls == []
    print("ok: sync_owui_config skips the network call without admin credentials")


def test_sync_never_writes_stdout() -> None:
    """The invariant that matters most: whatever happens inside
    sync_owui_config, the script's stdout contract (EMAIL=/PASSWORD=/
    SHIM_KEY=, nothing else) must stay untouched. Runs the failure and
    success paths with stdout captured and asserts it stayed empty."""
    def unreachable(req, timeout=None):
        raise urllib.error.URLError("down")

    def server_error(req, timeout=None):
        return FakeResponse(500, {"detail": "boom"})

    def happy_path(req, timeout=None):
        if req.full_url.endswith("signin"):
            return FakeResponse(200, {"token": "fake-jwt"})
        return FakeResponse(200, {})

    for fake_urlopen in (unreachable, server_error, happy_path):
        original = patch_urlopen(fake_urlopen)
        captured = io.StringIO()
        old_stdout = sys.stdout
        sys.stdout = captured
        try:
            seed_owui_e2e_user.sync_owui_config("hk_secret123")
        finally:
            sys.stdout = old_stdout
            restore_urlopen(original)
        assert captured.getvalue() == "", f"sync_owui_config wrote to stdout: {captured.getvalue()!r}"
    print("ok: sync_owui_config never writes to stdout, success or failure")


def main() -> None:
    os.environ["OWUI_ADMIN_EMAIL"] = "admin@example.com"
    os.environ["OWUI_ADMIN_PASSWORD"] = "pw"
    os.environ.setdefault("OWUI_BASE_URL", "http://localhost:3003")

    test_merge_appends_when_no_existing_entry()
    test_merge_preserves_other_entries_and_updates_matching_one()
    test_sync_full_flow_preserves_other_entries_in_final_post()
    test_sync_never_raises_on_unreachable_owui()
    test_sync_signin_fails_no_token()
    test_sync_config_update_non_200()
    test_sync_never_writes_stdout()

    del os.environ["OWUI_ADMIN_EMAIL"]
    del os.environ["OWUI_ADMIN_PASSWORD"]
    test_sync_skips_without_admin_credentials()

    print("ok: seed-owui-e2e-user.py OWUI config sync")


if __name__ == "__main__":
    main()

#!/usr/bin/env python3
"""Self-check for the shim-key rotation safety net in seed-owui-e2e-user.py.

Covers the OWUI config sync (fix for the live incident where OWUI's own
persisted OpenAI config drifts from a freshly rotated SHIM_KEY -- see PR #423
body), the .env rewrite, and the account scoping that keeps a scheduled CI
rotation from revoking a deployment's key. No framework, no network: mocks
urllib.request.urlopen and exercises the functions directly.
Run: python3 scripts/test_seed_owui_e2e_user.py
"""
import importlib.util
import io
import json
import os
import stat
import sys
import tempfile
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
    """No credential configured is not a failure: it is CI, which boots a fresh
    OWUI from .env.ci every run and has no persisted config to correct. The None
    return is what tells main() this consumer does not gate key revocation."""
    calls = []

    def fake_urlopen(req, timeout=None):
        calls.append(req)
        raise AssertionError("must not call network without admin credentials")

    original = patch_urlopen(fake_urlopen)
    saved = {k: os.environ.pop(k, None) for k in
             ("OWUI_ADMIN_TOKEN", "OWUI_ADMIN_EMAIL", "OWUI_ADMIN_PASSWORD")}
    try:
        assert seed_owui_e2e_user.sync_owui_config("hk_secret123") is None
    finally:
        restore_urlopen(original)
        for key, value in saved.items():
            if value is not None:
                os.environ[key] = value

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



# --- OWUI_ADMIN_TOKEN path -------------------------------------------------

def test_sync_uses_admin_token_and_skips_signin() -> None:
    """An OAuth-only Open WebUI has no password-authenticable admin, so the
    token path is the only one that works on a real deployment. It must not
    attempt a signin at all."""
    calls = []

    def fake_urlopen(req, timeout=None):
        calls.append(req)
        if req.full_url.endswith("/api/v1/auths/signin"):
            raise AssertionError("must not sign in when OWUI_ADMIN_TOKEN is set")
        if req.full_url.endswith("/openai/config") and req.get_method() == "GET":
            return FakeResponse(200, {})
        if req.full_url.endswith("/openai/config/update"):
            return FakeResponse(200, {"status": True})
        raise AssertionError(f"unexpected call: {req.full_url}")

    original = patch_urlopen(fake_urlopen)
    os.environ["OWUI_ADMIN_TOKEN"] = "owui-admin-token"
    try:
        assert seed_owui_e2e_user.sync_owui_config("hk_new") is True
    finally:
        del os.environ["OWUI_ADMIN_TOKEN"]
        restore_urlopen(original)

    assert len(calls) == 2, f"expected fetch + update only, got {len(calls)} calls"
    assert calls[1].headers.get("Authorization") == "Bearer owui-admin-token"
    print("ok: sync_owui_config authenticates with OWUI_ADMIN_TOKEN and skips signin")


def test_sync_reports_failure_so_the_old_key_survives() -> None:
    """False is the signal main() uses to hold off revoking the previous key.
    Returning None or True on a failed update would revoke a key Open WebUI is
    still configured with, which is the outage this ordering exists to stop."""
    def unreachable(req, timeout=None):
        raise urllib.error.URLError("connection refused")

    def update_rejected(req, timeout=None):
        if req.full_url.endswith("/openai/config") and req.get_method() == "GET":
            return FakeResponse(200, {})
        return FakeResponse(500, {"detail": "boom"})

    for fake_urlopen in (unreachable, update_rejected):
        original = patch_urlopen(fake_urlopen)
        os.environ["OWUI_ADMIN_TOKEN"] = "owui-admin-token"
        try:
            assert seed_owui_e2e_user.sync_owui_config("hk_new") is False
        finally:
            del os.environ["OWUI_ADMIN_TOKEN"]
            restore_urlopen(original)
    print("ok: sync_owui_config reports failure, so the previous key is not revoked")


# --- rewrite_env_file -----------------------------------------------------

def write_env(tmpdir: str, body: str, mode: int = 0o600) -> str:
    path = os.path.join(tmpdir, ".env")
    with open(path, "w", encoding="utf-8") as fh:
        fh.write(body)
    os.chmod(path, mode)
    return path


def test_rewrite_env_file_replaces_the_assignment_in_place() -> None:
    with tempfile.TemporaryDirectory() as tmpdir:
        path = write_env(tmpdir, "SUPABASE_URL=https://example.supabase.co\n"
                                 "OWUI_SHIM_KEY=hk_old\n"
                                 "GROQ_API_KEY=gsk_test\n")
        assert seed_owui_e2e_user.rewrite_env_file(path, "hk_new") is True
        lines = open(path, encoding="utf-8").read().splitlines()
        assert lines == [
            "SUPABASE_URL=https://example.supabase.co",
            "OWUI_SHIM_KEY=hk_new",
            "GROQ_API_KEY=gsk_test",
        ], lines
        # Mode preserved: this file carries every other secret the stack boots with.
        assert stat.S_IMODE(os.stat(path).st_mode) == 0o600
        # No temp file left behind next to it.
        assert os.listdir(tmpdir) == [".env"], os.listdir(tmpdir)
    print("ok: rewrite_env_file replaces the assignment, preserves mode, leaves no temp file")


def test_rewrite_env_file_appends_when_absent() -> None:
    for body in ("SUPABASE_URL=x\n", "SUPABASE_URL=x"):  # with and without trailing newline
        with tempfile.TemporaryDirectory() as tmpdir:
            path = write_env(tmpdir, body)
            assert seed_owui_e2e_user.rewrite_env_file(path, "hk_new") is True
            assert open(path, encoding="utf-8").read().splitlines() == [
                "SUPABASE_URL=x",
                "OWUI_SHIM_KEY=hk_new",
            ]
    print("ok: rewrite_env_file appends the assignment when the file has none")


def test_rewrite_env_file_ignores_commented_and_lookalike_lines() -> None:
    with tempfile.TemporaryDirectory() as tmpdir:
        path = write_env(tmpdir, "# OWUI_SHIM_KEY=hk_documented_example\n"
                                 "OWUI_SHIM_KEY_BACKUP=hk_other\n"
                                 "OWUI_SHIM_KEY=hk_old\n")
        assert seed_owui_e2e_user.rewrite_env_file(path, "hk_new") is True
        assert open(path, encoding="utf-8").read().splitlines() == [
            "# OWUI_SHIM_KEY=hk_documented_example",
            "OWUI_SHIM_KEY_BACKUP=hk_other",
            "OWUI_SHIM_KEY=hk_new",
        ]
    print("ok: rewrite_env_file rewrites only the real assignment")


def test_rewrite_env_file_replaces_every_duplicate() -> None:
    """docker compose --env-file honours the first assignment, so a stale
    duplicate left behind could shadow the new key after a later hand edit."""
    with tempfile.TemporaryDirectory() as tmpdir:
        path = write_env(tmpdir, "OWUI_SHIM_KEY=hk_old\nX=1\nOWUI_SHIM_KEY=hk_older\n")
        assert seed_owui_e2e_user.rewrite_env_file(path, "hk_new") is True
        assert open(path, encoding="utf-8").read().splitlines() == [
            "OWUI_SHIM_KEY=hk_new",
            "X=1",
            "OWUI_SHIM_KEY=hk_new",
        ]
    print("ok: rewrite_env_file replaces every duplicate assignment")


def test_rewrite_env_file_reports_a_missing_file() -> None:
    with tempfile.TemporaryDirectory() as tmpdir:
        assert seed_owui_e2e_user.rewrite_env_file(os.path.join(tmpdir, "absent"), "hk_new") is False
    print("ok: rewrite_env_file reports a missing file instead of creating one")


def test_rewrite_env_file_never_writes_stdout() -> None:
    with tempfile.TemporaryDirectory() as tmpdir:
        path = write_env(tmpdir, "OWUI_SHIM_KEY=hk_old\n")
        captured = io.StringIO()
        old_stdout = sys.stdout
        sys.stdout = captured
        try:
            seed_owui_e2e_user.rewrite_env_file(path, "hk_new")
            seed_owui_e2e_user.rewrite_env_file(os.path.join(tmpdir, "absent"), "hk_new")
        finally:
            sys.stdout = old_stdout
        assert captured.getvalue() == "", captured.getvalue()
    print("ok: rewrite_env_file never writes to stdout")


# --- account scoping ------------------------------------------------------

def test_account_slug_defaults_to_ci_and_is_overridable() -> None:
    """The account boundary is what keeps the nightly CI rotation from revoking
    a deployment's key, so the override has to exist and the default has to stay
    CI's own account."""
    argv = sys.argv
    saved = os.environ.pop("OWUI_SHIM_ACCOUNT_SLUG", None)
    try:
        sys.argv = ["seed-owui-e2e-user.py"]
        assert seed_owui_e2e_user.parse_args().account_slug == "owui-e2e-shim"
        sys.argv = ["seed-owui-e2e-user.py", "--account-slug", "owui-shim-demo-box"]
        args = seed_owui_e2e_user.parse_args()
        assert args.account_slug == "owui-shim-demo-box"
        assert args.env_file == ""
        sys.argv = ["seed-owui-e2e-user.py", "--env-file", "/tmp/.env"]
        assert seed_owui_e2e_user.parse_args().env_file == "/tmp/.env"
        os.environ["OWUI_SHIM_ACCOUNT_SLUG"] = "owui-shim-from-env"
        sys.argv = ["seed-owui-e2e-user.py"]
        assert seed_owui_e2e_user.parse_args().account_slug == "owui-shim-from-env"
    finally:
        sys.argv = argv
        os.environ.pop("OWUI_SHIM_ACCOUNT_SLUG", None)
        if saved is not None:
            os.environ["OWUI_SHIM_ACCOUNT_SLUG"] = saved
    print("ok: --account-slug defaults to the CI account and can be overridden")


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
    test_sync_uses_admin_token_and_skips_signin()
    test_sync_reports_failure_so_the_old_key_survives()
    test_rewrite_env_file_replaces_the_assignment_in_place()
    test_rewrite_env_file_appends_when_absent()
    test_rewrite_env_file_ignores_commented_and_lookalike_lines()
    test_rewrite_env_file_replaces_every_duplicate()
    test_rewrite_env_file_reports_a_missing_file()
    test_rewrite_env_file_never_writes_stdout()
    test_account_slug_defaults_to_ci_and_is_overridable()

    del os.environ["OWUI_ADMIN_EMAIL"]
    del os.environ["OWUI_ADMIN_PASSWORD"]
    test_sync_skips_without_admin_credentials()

    print("ok: seed-owui-e2e-user.py OWUI config sync")


if __name__ == "__main__":
    main()

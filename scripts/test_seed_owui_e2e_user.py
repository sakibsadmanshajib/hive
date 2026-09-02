#!/usr/bin/env python3
"""Self-check for the shim-key rotation safety net in seed-owui-e2e-user.py.

Covers the OWUI config sync (fix for the live incident where OWUI's own
persisted OpenAI config drifts from a freshly rotated SHIM_KEY -- see PR #423
body), the .env rewrite, and the account scoping that keeps a scheduled CI
rotation from revoking a deployment's key. No framework, no network: mocks
urllib.request.urlopen and exercises the functions directly.
Run: python3 scripts/test_seed_owui_e2e_user.py
"""
import ast
import datetime
import hashlib
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


# --- tenant billing mapping (issue #717, then #1599) -----------------------
#
# The six tests that lived here moved to scripts/test_shared_billing_mapping.py
# along with the implementation. Both seeders write public.tenant_billing_accounts
# and the two copies of that rule had already drifted once: this script learned
# to write the row in #717, seed-demo-owner.py had not, and #1599 is the same
# defect reported again. One implementation, one self-check, and that file also
# asserts that neither seeder has grown a private copy again.


def test_tenant_slug_defaults_to_ci_and_is_overridable() -> None:
    """Two shim accounts cannot share one tenant (1:1 in both directions), so a
    deployment with its own --account-slug needs its own tenant too."""
    argv = sys.argv
    saved = os.environ.pop("OWUI_TENANT_SLUG", None)
    try:
        sys.argv = ["seed-owui-e2e-user.py"]
        assert seed_owui_e2e_user.parse_args().tenant_slug == "owui-e2e"
        sys.argv = ["seed-owui-e2e-user.py", "--tenant-slug", "owui-demo-box"]
        assert seed_owui_e2e_user.parse_args().tenant_slug == "owui-demo-box"
        os.environ["OWUI_TENANT_SLUG"] = "owui-from-env"
        sys.argv = ["seed-owui-e2e-user.py"]
        assert seed_owui_e2e_user.parse_args().tenant_slug == "owui-from-env"
    finally:
        sys.argv = argv
        os.environ.pop("OWUI_TENANT_SLUG", None)
        if saved is not None:
            os.environ["OWUI_TENANT_SLUG"] = saved
    print("ok: --tenant-slug defaults to the CI tenant and can be overridden")


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


def test_password_is_never_rotated_on_an_existing_account() -> None:
    """An account that already exists keeps its password unless the caller
    explicitly supplies one. This script runs unattended from the nightly
    workflow, and rotating a shared account revokes every session that any
    concurrent run is holding on it (docs/live-test-auth.md). Mirrors the
    same guard in scripts/test_seed_demo_owner.py."""
    password_to_set = seed_owui_e2e_user.password_to_set
    assert password_to_set(True, "", "generated-pw") is None
    assert password_to_set(True, "   ", "generated-pw") is None
    assert password_to_set(True, "explicit-pw", "generated-pw") == "explicit-pw"

    # A brand-new account has no session to break and no credential to keep.
    assert password_to_set(False, "", "generated-pw") == "generated-pw"
    assert password_to_set(False, "explicit-pw", "generated-pw") == "explicit-pw"
    print("ok: password_to_set never rotates an existing account by default")


def test_run_key_namespaces_the_fixture_addresses() -> None:
    """A run key gives each run its own users, so the guard above never has to
    refuse anything in CI: there is no shared account left to rotate."""
    with_run_key = seed_owui_e2e_user.with_run_key
    assert with_run_key("owui-e2e@hive-e2e.invalid", "") == "owui-e2e@hive-e2e.invalid"
    assert with_run_key("owui-e2e@hive-e2e.invalid", "  ") == "owui-e2e@hive-e2e.invalid"
    assert (
        with_run_key("owui-e2e@hive-e2e.invalid", "12345-1")
        == "owui-e2e+12345-1@hive-e2e.invalid"
    )
    # Two attempts of the same workflow run must not collide.
    assert with_run_key("owui-e2e@hive-e2e.invalid", "12345-1") != with_run_key(
        "owui-e2e@hive-e2e.invalid", "12345-2"
    )
    print("ok: with_run_key namespaces the fixture addresses per run")


def test_stale_key_cutoff_is_backdated_not_now() -> None:
    """The shim key delete is bounded by age, not by "everything except mine".
    Two runs overlap (the schedule and a labelled pull request are in different
    concurrency groups), and an identity-bounded delete had each revoking the
    other's key mid-flight, which is the outage in .wolf/cerebrum.md."""
    now = datetime.datetime(2026, 8, 11, 12, 0, tzinfo=datetime.timezone.utc)
    cutoff = seed_owui_e2e_user.stale_key_cutoff_iso(now)
    assert cutoff == "2026-08-11T06:00:00+00:00", cutoff
    # A key minted seconds ago sorts after the cutoff, so PostgREST's
    # created_at=lt.<cutoff> filter cannot match it.
    assert now.isoformat() > cutoff
    print("ok: stale_key_cutoff_iso spares a concurrent run's fresh key")


def test_sweep_only_deletes_stale_run_scoped_fixture_users() -> None:
    """The sweep must never reach the shared base address or a real account,
    and never this run's own users."""
    old = "2020-01-01T00:00:00+00:00"
    fresh = datetime.datetime.now(datetime.timezone.utc).isoformat()
    users = [
        {"id": "stale", "email": "owui-e2e+111-1@hive-e2e.invalid", "created_at": old},
        {"id": "stale-bootstrap", "email": "owui-e2e-bootstrap+111-1@hive-e2e.invalid", "created_at": old},
        {"id": "mine", "email": "owui-e2e+222-1@hive-e2e.invalid", "created_at": old},
        {"id": "recent", "email": "owui-e2e+333-1@hive-e2e.invalid", "created_at": fresh},
        {"id": "shared-base", "email": "owui-e2e@hive-e2e.invalid", "created_at": old},
        {"id": "customer", "email": "owui-e2e+x@real-customer.example", "created_at": old},
        {"id": "lookalike", "email": "owui-e2e-someone-else@hive-e2e.invalid", "created_at": old},
    ]
    deleted = []

    def fake_request(base, headers, method, path, body=None, params=None, prefer=None):
        if method == "GET":
            return 200, {"users": users}
        assert method == "DELETE", method
        deleted.append(path.rsplit("/", 1)[-1])
        return 204, None

    original = seed_owui_e2e_user.request
    seed_owui_e2e_user.request = fake_request
    try:
        seed_owui_e2e_user.sweep_stale_fixture_users("gotrue", {}, "222-1")
    finally:
        seed_owui_e2e_user.request = original

    assert sorted(deleted) == ["stale", "stale-bootstrap"], deleted
    print("ok: sweep removes only stale run-scoped fixture users")


def test_sweep_is_a_no_op_without_a_run_key() -> None:
    """No run key means the shared identity, and nothing about it is sweepable."""
    def explode(*args, **kwargs):
        raise AssertionError("sweep must not call out without a run key")

    original = seed_owui_e2e_user.request
    seed_owui_e2e_user.request = explode
    try:
        seed_owui_e2e_user.sweep_stale_fixture_users("gotrue", {}, "")
    finally:
        seed_owui_e2e_user.request = original
    print("ok: sweep is inert without a run key")


# --- account scope: a seeding run may not touch a deployment's credential ---
#
# Issue #560. The account boundary above is the mechanism that keeps the
# nightly rotation off a long-lived deployment's key, but until these guards it
# was a convention stated in prose: nothing stopped a run from rotating the CI
# account while a deployment carried a key on it, and nothing stopped a run
# from revoking keys on a deployment account without updating that deployment.
# Both are refused now, before anything is minted or revoked.

def _scope_env(**overrides) -> dict:
    """Snapshot and set the three OWUI admin variables the guard reads."""
    saved = {k: os.environ.pop(k, None) for k in
             ("OWUI_ADMIN_TOKEN", "OWUI_ADMIN_EMAIL", "OWUI_ADMIN_PASSWORD")}
    for k, v in overrides.items():
        os.environ[k] = v
    return saved


def _restore_scope_env(saved: dict) -> None:
    for k, v in saved.items():
        os.environ.pop(k, None)
        if v is not None:
            os.environ[k] = v


def test_ci_account_refuses_a_long_lived_consumer() -> None:
    """A deployment may not be put on the account CI rotates nightly. This is
    the configuration that produced the outage: one account, rotated on a
    schedule by a run that knows nothing about the deployment carrying it."""
    saved = _scope_env()
    try:
        for env_file, sync in (("/tmp/.env", {}), ("", {"OWUI_ADMIN_TOKEN": "t"}),
                               ("", {"OWUI_ADMIN_EMAIL": "a@b.c", "OWUI_ADMIN_PASSWORD": "pw"})):
            _restore_scope_env(saved)
            _scope_env(**sync)
            err = io.StringIO()
            stderr, sys.stderr = sys.stderr, err
            try:
                seed_owui_e2e_user.assert_account_scope("owui-e2e-shim", env_file)
            except SystemExit as exc:
                assert exc.code != 0, exc.code
            else:
                raise AssertionError(f"expected a refusal for env_file={env_file!r} sync={sync!r}")
            finally:
                sys.stderr = stderr
            assert "--account-slug" in err.getvalue(), err.getvalue()
    finally:
        _restore_scope_env(saved)
    print("ok: the CI rotation account refuses a long-lived consumer")


def test_deployment_account_refuses_a_run_that_updates_nothing() -> None:
    """The other half: revocation on a non-CI account is only ever allowed to
    the run that also replaces the value every consumer carries."""
    saved = _scope_env()
    err = io.StringIO()
    stderr, sys.stderr = sys.stderr, err
    try:
        seed_owui_e2e_user.assert_account_scope("hive-demo-owui-shim", "")
    except SystemExit as exc:
        assert exc.code != 0, exc.code
    else:
        raise AssertionError("expected a refusal")
    finally:
        sys.stderr = stderr
        _restore_scope_env(saved)
    assert "--env-file" in err.getvalue(), err.getvalue()
    print("ok: a deployment account refuses a run with no consumer to update")


def test_the_two_supported_shapes_are_allowed() -> None:
    """CI's shape (the rotation account, no consumer) and a deployment's shape
    (its own account, a consumer this run updates) both pass."""
    saved = _scope_env()
    try:
        seed_owui_e2e_user.assert_account_scope("owui-e2e-shim", "")
        seed_owui_e2e_user.assert_account_scope("hive-demo-owui-shim", "/tmp/.env")
        _scope_env(OWUI_ADMIN_TOKEN="t")
        seed_owui_e2e_user.assert_account_scope("hive-demo-owui-shim", "")
    finally:
        _restore_scope_env(saved)
    print("ok: both supported shapes pass the scope guard")


def test_scope_is_asserted_before_any_network_call() -> None:
    """The guard is worth nothing if it runs after the mint. Drives main()
    itself with a refused configuration and asserts nothing was requested."""
    def explode(*args, **kwargs):
        raise AssertionError("main() reached the network on a refused configuration")

    saved = _scope_env()
    argv, stderr = sys.argv, sys.stderr
    original = patch_urlopen(explode)
    saved_url = os.environ.get("SUPABASE_URL")
    saved_key = os.environ.get("SUPABASE_SERVICE_ROLE_KEY")
    os.environ["SUPABASE_URL"] = "http://supabase.invalid"
    os.environ["SUPABASE_SERVICE_ROLE_KEY"] = "service-role"
    sys.argv = ["seed-owui-e2e-user.py", "--account-slug", "owui-e2e-shim", "--env-file", "/tmp/.env"]
    sys.stderr = io.StringIO()
    try:
        seed_owui_e2e_user.main()
    except SystemExit as exc:
        assert exc.code != 0, exc.code
    else:
        raise AssertionError("expected main() to refuse")
    finally:
        restore_urlopen(original)
        sys.argv, sys.stderr = argv, stderr
        _restore_scope_env(saved)
        for name, value in (("SUPABASE_URL", saved_url), ("SUPABASE_SERVICE_ROLE_KEY", saved_key)):
            os.environ.pop(name, None)
            if value is not None:
                os.environ[name] = value
    print("ok: the scope guard runs before anything is minted or revoked")


def test_revocation_only_ever_targets_keys_this_script_minted() -> None:
    """Third layer, and the one that survives a key minted by hand: the cleanup
    filters on the nickname this script writes, so a foreign key sharing the
    account is never revoked by a rotation that knows nothing about it."""
    source = (Path(__file__).parent / "seed-owui-e2e-user.py").read_text()
    tree = ast.parse(source)
    deletes = [
        node for node in ast.walk(tree)
        if isinstance(node, ast.Call)
        and isinstance(node.func, ast.Name) and node.func.id == "request"
        and any(isinstance(a, ast.Constant) and a.value == "DELETE" for a in node.args)
        and any(isinstance(a, ast.Constant) and a.value == "/api_keys" for a in node.args)
    ]
    assert len(deletes) == 1, f"expected exactly one api_keys deletion, found {len(deletes)}"
    params = next((kw.value for kw in deletes[0].keywords if kw.arg == "params"), None)
    assert params is not None, "the api_keys deletion passes no params at all"
    keys = {k.value for k in params.keys if isinstance(k, ast.Constant)}
    assert "nickname" in keys, f"the deletion is not filtered by nickname: {sorted(keys)}"
    assert "account_id" in keys, f"the deletion is not filtered by account: {sorted(keys)}"
    print("ok: key revocation is filtered to this script's own nickname")


def test_the_nightly_workflow_still_names_the_ci_account() -> None:
    """A static guard on the caller, not the callee. The nightly is the run that
    rotates on a schedule; pointing it at a deployment account is the edit that
    reopens issue #560, and the guard above would only catch it if that run also
    configured a consumer, which a GitHub runner never does."""
    workflow = (Path(__file__).parent.parent / ".github/workflows/owui-nightly.yml").read_text()
    assert "seed-owui-e2e-user.py --account-slug owui-e2e-shim" in workflow, (
        "the nightly no longer passes the reserved CI account slug explicitly"
    )
    print("ok: the nightly rotates the reserved CI account and nothing else")


def test_a_padded_reserved_slug_cannot_slip_past_the_guard() -> None:
    """Whitespace is not a way around the reserved account. Unstripped, a padded
    slug would both miss the guard's comparison and upsert a second account row
    nobody rotates, which looks like CI's and is not."""
    argv = sys.argv
    saved = _scope_env(OWUI_ADMIN_TOKEN="t")
    stderr, sys.stderr = sys.stderr, io.StringIO()
    try:
        sys.argv = ["seed-owui-e2e-user.py", "--account-slug", "  owui-e2e-shim  "]
        args = seed_owui_e2e_user.parse_args()
        assert args.account_slug == "owui-e2e-shim", args.account_slug
        try:
            seed_owui_e2e_user.assert_account_scope(args.account_slug, args.env_file)
        except SystemExit as exc:
            assert exc.code != 0, exc.code
        else:
            raise AssertionError("a padded reserved slug was allowed a consumer")
    finally:
        sys.argv, sys.stderr = argv, stderr
        _restore_scope_env(saved)
    print("ok: a padded reserved account slug is still the reserved account")


def test_an_empty_account_slug_is_refused() -> None:
    """An empty slug misses the reserved comparison, so with a consumer
    configured it used to pass the guard and then mint a key on an account whose
    slug is the empty string: a garbage account nothing rotates and nobody
    owns, holding a credential a deployment was just handed."""
    saved = _scope_env(OWUI_ADMIN_TOKEN="t")
    stderr, sys.stderr = sys.stderr, io.StringIO()
    try:
        try:
            seed_owui_e2e_user.assert_account_scope("", "")
        except SystemExit as exc:
            assert exc.code == 2, exc.code
        else:
            raise AssertionError("an empty account slug was allowed")
    finally:
        sys.stderr = stderr
        _restore_scope_env(saved)
    print("ok: an empty account slug is refused")


def test_a_cased_reserved_slug_is_still_the_reserved_account() -> None:
    """public.accounts.slug is case sensitive, so `OWUI-E2E-SHIM` would miss the
    reserved arm, be treated as a deployment account and upsert a near-duplicate
    row nothing rotates. Same confusion the whitespace strip exists to prevent."""
    saved = _scope_env(OWUI_ADMIN_TOKEN="t")
    stderr, sys.stderr = sys.stderr, io.StringIO()
    try:
        try:
            seed_owui_e2e_user.assert_account_scope("OWUI-E2E-SHIM", "")
        except SystemExit as exc:
            assert exc.code == 2, exc.code
        else:
            raise AssertionError("a cased reserved slug was allowed a consumer")
    finally:
        sys.stderr = stderr
        _restore_scope_env(saved)
    print("ok: a cased reserved account slug is still the reserved account")


# --- the nickname is a property, not a coincidence ------------------------

def test_key_nickname_is_derived_from_the_account() -> None:
    """The cleanup filter used to hold only because the box's live key happened
    to carry a different name than the constant this script wrote. That
    coincidence dies the first time anyone follows the documented remedy, since
    the mint writes the nickname unconditionally. Derived, it cannot."""
    nickname = seed_owui_e2e_user.key_nickname
    assert nickname("owui-e2e-shim") == seed_owui_e2e_user.SHIM_KEY_NICKNAME
    assert nickname("hive-demo-owui-shim") == "hive-demo-owui-shim-key"
    # Two accounts can never be handed the same nickname, so CI's cleanup can
    # never match a deployment's key even if the two ever shared an account.
    assert nickname("owui-e2e-shim") != nickname("hive-demo-owui-shim")
    print("ok: the key nickname is derived from the account slug")


def test_the_derived_nickname_reproduces_the_live_key_on_the_box() -> None:
    """The property is only worth anything if it agrees with the deployment it
    has to protect. The demo box's live key, read out of public.api_keys and
    recorded in this PR's proof capture, is `hive-demo-owui-shim-key` on account
    `hive-demo-owui-shim`. So the documented remedy reproduces the name already
    on the box rather than replacing it, and the run that mints the replacement
    revokes the key it replaced instead of leaving it active forever."""
    capture = (
        Path(__file__).parent.parent
        / "docs/proof/shim-key-revocation-560-2026-09-02/capture.md"
    ).read_text()
    live = "hive-demo-owui-shim|hive-demo-owui-shim-key|active"
    assert live in capture, "the recorded live row no longer names the key this derivation must match"
    account, expected = live.split("|")[0], live.split("|")[1]
    assert seed_owui_e2e_user.key_nickname(account) == expected
    print("ok: the derived nickname is the one the live key already carries")


def test_the_mint_and_the_cleanup_derive_the_same_nickname() -> None:
    """Both call sites must use the derivation. A mint that writes the constant
    while the cleanup filters on the derived name (or the reverse) revokes
    either nothing or the wrong thing, and both are silent."""
    source = (Path(__file__).parent / "seed-owui-e2e-user.py").read_text()
    tree = ast.parse(source)

    def nickname_value(call, kwarg):
        mapping = next((kw.value for kw in call.keywords if kw.arg == kwarg), None)
        if not isinstance(mapping, ast.Dict):
            return None
        for key, value in zip(mapping.keys, mapping.values):
            if isinstance(key, ast.Constant) and key.value == "nickname":
                return value
        return None

    def derives_nickname(node) -> bool:
        """True for key_nickname(...) and for f-strings interpolating it."""
        if node is None:
            return False
        return any(
            isinstance(inner, ast.Call)
            and isinstance(inner.func, ast.Name)
            and inner.func.id == "key_nickname"
            for inner in ast.walk(node)
        )

    calls = [
        node for node in ast.walk(tree)
        if isinstance(node, ast.Call)
        and isinstance(node.func, ast.Name) and node.func.id == "request"
        and any(isinstance(a, ast.Constant) and a.value == "/api_keys" for a in node.args)
    ]
    mints = [c for c in calls if any(isinstance(a, ast.Constant) and a.value == "POST" for a in c.args)]
    deletes = [c for c in calls if any(isinstance(a, ast.Constant) and a.value == "DELETE" for a in c.args)]
    assert len(mints) == 1 and len(deletes) == 1, (len(mints), len(deletes))
    assert derives_nickname(nickname_value(mints[0], "body")), "the mint no longer derives its nickname"
    assert derives_nickname(nickname_value(deletes[0], "params")), "the cleanup no longer derives its nickname"
    print("ok: the mint and the cleanup both derive the nickname from the account")


# --- --env-file proves a rewrite, not that it was the right file ----------

def test_read_env_assignment_reads_what_the_file_was_carrying() -> None:
    with tempfile.TemporaryDirectory() as d:
        path = os.path.join(d, ".env")
        with open(path, "w", encoding="utf-8") as fh:
            fh.write("# OWUI_SHIM_KEY=commented-out\nOTHER=1\nOWUI_SHIM_KEY=hk_old\nOWUI_SHIM_KEY=hk_dup\n")
        assert seed_owui_e2e_user.read_env_assignment(path) == "hk_old"

        with open(path, "w", encoding="utf-8") as fh:
            fh.write("OTHER=1\nOWUI_SHIM_KEY=\n")
        assert seed_owui_e2e_user.read_env_assignment(path) == ""
        assert seed_owui_e2e_user.read_env_assignment(os.path.join(d, "nope.env")) == ""
    print("ok: read_env_assignment reports what the file was carrying")


def test_a_scratch_env_file_earns_no_revocation() -> None:
    """`--env-file /tmp/scratch.env` rewrites a file, satisfies the scope guard
    and satisfies the consumer-sync check, and the revocation then takes down
    whoever really holds this account's key: issue #560 reached through the arm
    the guard allows. The proof that this run replaced the key it is about to
    revoke is that the value it overwrote is a key on this very account."""
    seen = []

    def found(base, headers, method, path, body=None, params=None, prefer=None):
        seen.append((method, path, params))
        return 200, [{"id": "key-1"}]

    def not_found(base, headers, method, path, body=None, params=None, prefer=None):
        seen.append((method, path, params))
        return 200, []

    original = seed_owui_e2e_user.request
    try:
        # A file carrying nothing (first-time setup, or the wrong file) never
        # reaches the database: there is no old key whose revocation this run
        # could have earned.
        seed_owui_e2e_user.request = found
        assert seed_owui_e2e_user.env_file_carried_this_accounts_key({}, {}, "acct-1", "") is False
        assert seen == [], seen

        # A value that hashes to no key on this account: another deployment's
        # .env, or a scratch path someone put an OWUI_SHIM_KEY line in.
        seed_owui_e2e_user.request = not_found
        assert seed_owui_e2e_user.env_file_carried_this_accounts_key({}, {}, "acct-1", "hk_foreign") is False

        # The real deployment's .env: the value it held is a key on this account.
        seed_owui_e2e_user.request = found
        assert seed_owui_e2e_user.env_file_carried_this_accounts_key({}, {}, "acct-1", "hk_real") is True
        method, path, params = seen[-1]
        assert (method, path) == ("GET", "/api_keys"), (method, path)
        assert params["account_id"] == "eq.acct-1", params
        assert params["token_hash"] == "eq." + hashlib.sha256(b"hk_real").hexdigest(), params
    finally:
        seed_owui_e2e_user.request = original
    print("ok: only the file that was carrying this account's key earns a revocation")


def test_the_revocation_is_gated_on_that_check() -> None:
    """And the check has to sit in front of the delete, not merely exist."""
    source = (Path(__file__).parent / "seed-owui-e2e-user.py").read_text()
    tree = ast.parse(source)
    branches = [
        node for node in ast.walk(tree)
        if isinstance(node, ast.If)
        and any(
            isinstance(call, ast.Call)
            and isinstance(call.func, ast.Name) and call.func.id == "request"
            and any(isinstance(a, ast.Constant) and a.value == "DELETE" for a in call.args)
            for stmt in node.orelse for call in ast.walk(stmt)
        )
    ]
    assert branches, "no branch guards the api_keys deletion at all"
    # The innermost one: `if consumer_failed / elif env_file_unproven / else
    # delete` nests, and it is the last test before the delete that matters.
    gate = max(branches, key=lambda node: node.lineno)
    guarded_by = {n.id for n in ast.walk(gate.test) if isinstance(n, ast.Name)}
    assert "env_file_unproven" in guarded_by, (
        f"the deletion is no longer gated on the env-file check: {sorted(guarded_by)}"
    )
    assigned = [
        node for node in ast.walk(tree)
        if isinstance(node, ast.Call)
        and isinstance(node.func, ast.Name)
        and node.func.id == "env_file_carried_this_accounts_key"
    ]
    assert assigned, "env_file_unproven is no longer computed from the account check"
    print("ok: the deletion is gated on the env file having carried this account's key")


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
    test_tenant_slug_defaults_to_ci_and_is_overridable()
    test_password_is_never_rotated_on_an_existing_account()
    test_run_key_namespaces_the_fixture_addresses()
    test_stale_key_cutoff_is_backdated_not_now()
    test_sweep_only_deletes_stale_run_scoped_fixture_users()
    test_sweep_is_a_no_op_without_a_run_key()
    test_ci_account_refuses_a_long_lived_consumer()
    test_deployment_account_refuses_a_run_that_updates_nothing()
    test_the_two_supported_shapes_are_allowed()
    test_scope_is_asserted_before_any_network_call()
    test_revocation_only_ever_targets_keys_this_script_minted()
    test_the_nightly_workflow_still_names_the_ci_account()
    test_a_padded_reserved_slug_cannot_slip_past_the_guard()
    test_an_empty_account_slug_is_refused()
    test_a_cased_reserved_slug_is_still_the_reserved_account()
    test_key_nickname_is_derived_from_the_account()
    test_the_derived_nickname_reproduces_the_live_key_on_the_box()
    test_the_mint_and_the_cleanup_derive_the_same_nickname()
    test_read_env_assignment_reads_what_the_file_was_carrying()
    test_a_scratch_env_file_earns_no_revocation()
    test_the_revocation_is_gated_on_that_check()

    del os.environ["OWUI_ADMIN_EMAIL"]
    del os.environ["OWUI_ADMIN_PASSWORD"]
    test_sync_skips_without_admin_credentials()

    print("ok: seed-owui-e2e-user.py OWUI config sync")


if __name__ == "__main__":
    main()

#!/usr/bin/env python3
"""Self-check for sync_owui_config in seed-owui-e2e-user.py (fix for the
live incident where OWUI's own persisted OpenAI config drifts from a
freshly rotated SHIM_KEY -- see PR body). No framework, no network:
mocks urllib.request.urlopen and exercises owui_config_body and
sync_owui_config directly. Run: python3 scripts/test_seed_owui_e2e_user.py
"""
import importlib.util
import json
import os
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


def test_body_shape() -> None:
    body = seed_owui_e2e_user.owui_config_body("hk_secret123")
    assert body["ENABLE_OPENAI_API"] is True
    assert body["OPENAI_API_KEYS"] == ["hk_secret123"]
    assert body["OPENAI_API_BASE_URLS"] == ["http://edge-api:8080/v1"]
    assert body["OPENAI_API_CONFIGS"] == {"0": {"enable": True}}
    print("ok: owui_config_body shape")


def test_sync_posts_signin_then_config_update() -> None:
    calls = []

    def fake_urlopen(req, timeout=None):
        calls.append(req)
        if req.full_url.endswith("/api/v1/auths/signin"):
            return FakeResponse(200, {"token": "fake-jwt"})
        if req.full_url.endswith("/openai/config/update"):
            return FakeResponse(200, {"status": True})
        raise AssertionError(f"unexpected url: {req.full_url}")

    original = urllib.request.urlopen
    urllib.request.urlopen = fake_urlopen
    try:
        seed_owui_e2e_user.sync_owui_config("hk_secret123")
    finally:
        urllib.request.urlopen = original

    assert len(calls) == 2
    signin_body = json.loads(calls[0].data)
    assert signin_body == {"email": "admin@example.com", "password": "pw"}
    config_body = json.loads(calls[1].data)
    assert config_body["OPENAI_API_KEYS"] == ["hk_secret123"]
    assert calls[1].headers.get("Authorization") == "Bearer fake-jwt"
    print("ok: sync_owui_config posts signin then config update with correct bodies")


def test_sync_never_raises_on_failure() -> None:
    def fake_urlopen(req, timeout=None):
        raise urllib.error.URLError("connection refused")

    original = urllib.request.urlopen
    urllib.request.urlopen = fake_urlopen
    try:
        seed_owui_e2e_user.sync_owui_config("hk_secret123")  # must not raise
    finally:
        urllib.request.urlopen = original
    print("ok: sync_owui_config does not raise when OWUI is unreachable")


def test_sync_skips_without_admin_credentials() -> None:
    calls = []

    def fake_urlopen(req, timeout=None):
        calls.append(req)
        raise AssertionError("must not call network without admin credentials")

    original = urllib.request.urlopen
    urllib.request.urlopen = fake_urlopen
    old_email = os.environ.pop("OWUI_ADMIN_EMAIL", None)
    old_password = os.environ.pop("OWUI_ADMIN_PASSWORD", None)
    try:
        seed_owui_e2e_user.sync_owui_config("hk_secret123")
    finally:
        urllib.request.urlopen = original
        if old_email is not None:
            os.environ["OWUI_ADMIN_EMAIL"] = old_email
        if old_password is not None:
            os.environ["OWUI_ADMIN_PASSWORD"] = old_password

    assert calls == []
    print("ok: sync_owui_config skips the network call without admin credentials")


def main() -> None:
    os.environ["OWUI_ADMIN_EMAIL"] = "admin@example.com"
    os.environ["OWUI_ADMIN_PASSWORD"] = "pw"
    os.environ.setdefault("OWUI_BASE_URL", "http://localhost:3003")

    test_body_shape()
    test_sync_posts_signin_then_config_update()
    test_sync_never_raises_on_failure()

    del os.environ["OWUI_ADMIN_EMAIL"]
    del os.environ["OWUI_ADMIN_PASSWORD"]
    test_sync_skips_without_admin_credentials()

    print("ok: seed-owui-e2e-user.py OWUI config sync")


if __name__ == "__main__":
    main()

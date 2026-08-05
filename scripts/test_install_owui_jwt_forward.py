#!/usr/bin/env python3
"""Self-check for the hive_jwt_forward Open WebUI Function installer (#556).

Covers the three things the issue is actually about: that the install happens
automatically, that re-running a deploy does not corrupt or duplicate it, and
that success is decided by re-reading the end state rather than by a 2xx on the
install call. Also asserts the deploy workflow really invokes the script, since
an installer nothing calls is the exact defect #556 reports.

No framework, no network: mocks urllib.request.urlopen and exercises the
functions directly, same shape as scripts/test_seed_owui_e2e_user.py.
Run: python3 scripts/test_install_owui_jwt_forward.py
"""
import importlib.util
import io
import json
import sys
import urllib.error
import urllib.request
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent

spec = importlib.util.spec_from_file_location(
    "install_owui_jwt_forward", REPO_ROOT / "scripts" / "install-owui-jwt-forward.py"
)
installer = importlib.util.module_from_spec(spec)
spec.loader.exec_module(installer)

BASE = "http://owui.test"
TOKEN = "admin-token"
CONTENT = "class Filter:\n    pass\n"
FUNCTION_PATH = "/api/v1/functions/id/hive_jwt_forward"


class FakeResponse:
    def __init__(self, status, body):
        self.status = status
        self._raw = json.dumps(body).encode() if body is not None else b""

    def read(self):
        return self._raw

    def __enter__(self):
        return self

    def __exit__(self, *exc):
        return False


def http_error(url, code, detail):
    """An HTTPError the installer can .read(), the way a real one behaves."""
    return urllib.error.HTTPError(
        url, code, detail, {}, io.BytesIO(json.dumps({"detail": detail}).encode())
    )


class FakeOwui:
    """Minimal stand-in for Open WebUI's Functions API.

    Reproduces the two behaviours that make a naive installer wrong: an
    unknown id answers 401 (not 404), and both toggle endpoints FLIP their
    flag rather than set it.
    """

    def __init__(self, state=None):
        self.state = state
        self.calls = []

    def __call__(self, request, timeout=None):
        method = request.method
        path = request.full_url[len(BASE) :]
        body = json.loads(request.data.decode()) if request.data else None
        self.calls.append((method, path))

        if request.headers.get("Authorization") != f"Bearer {TOKEN}":
            raise http_error(request.full_url, 403, "Forbidden")

        if method == "GET" and path == FUNCTION_PATH:
            if self.state is None:
                raise http_error(request.full_url, 401, "Not found")
            return FakeResponse(200, self.state)

        if method == "POST" and path == "/api/v1/functions/create":
            if self.state is not None:
                raise http_error(request.full_url, 400, "Id taken")
            self.state = {
                "id": body["id"],
                "name": body["name"],
                "content": body["content"],
                "type": "filter",
                "is_active": False,
                "is_global": False,
            }
            return FakeResponse(200, self.state)

        if method == "POST" and path == f"{FUNCTION_PATH}/update":
            self.state = {**self.state, "content": body["content"], "name": body["name"]}
            return FakeResponse(200, self.state)

        if method == "POST" and path == f"{FUNCTION_PATH}/toggle":
            self.state = {**self.state, "is_active": not self.state["is_active"]}
            return FakeResponse(200, self.state)

        if method == "POST" and path == f"{FUNCTION_PATH}/toggle/global":
            self.state = {**self.state, "is_global": not self.state["is_global"]}
            return FakeResponse(200, self.state)

        raise AssertionError(f"unexpected call: {method} {path}")


def run_with(fake):
    original = urllib.request.urlopen
    urllib.request.urlopen = fake
    try:
        installer.ensure_installed(BASE, TOKEN, CONTENT)
        return installer.verify(BASE, TOKEN, CONTENT)
    finally:
        urllib.request.urlopen = original


def installed(**overrides):
    state = {
        "id": "hive_jwt_forward",
        "name": "Hive JWT Forward",
        "content": CONTENT,
        "type": "filter",
        "is_active": True,
        "is_global": True,
    }
    state.update(overrides)
    return state


def test_absent_function_is_created_activated_and_globalized() -> None:
    fake = FakeOwui(state=None)
    final = run_with(fake)
    assert final["is_active"] is True
    assert final["is_global"] is True
    assert final["content"] == CONTENT
    assert ("POST", "/api/v1/functions/create") in fake.calls
    assert ("POST", f"{FUNCTION_PATH}/toggle") in fake.calls
    assert ("POST", f"{FUNCTION_PATH}/toggle/global") in fake.calls


def test_rerunning_a_deploy_writes_nothing() -> None:
    """The idempotency guarantee. Toggling flips, so an unconditional toggle
    would switch the filter off on the second deploy."""
    fake = FakeOwui(state=installed())
    final = run_with(fake)
    assert final["is_active"] is True
    assert final["is_global"] is True
    writes = [call for call in fake.calls if call[0] != "GET"]
    assert writes == [], f"a no-op run wrote: {writes}"


def test_inactive_function_is_activated_without_touching_global() -> None:
    fake = FakeOwui(state=installed(is_active=False))
    final = run_with(fake)
    assert final["is_active"] is True
    assert final["is_global"] is True
    assert fake.calls.count(("POST", f"{FUNCTION_PATH}/toggle/global")) == 0


def test_stale_content_is_refreshed_from_source() -> None:
    fake = FakeOwui(state=installed(content="class Filter:\n    # old\n    pass\n"))
    final = run_with(fake)
    assert final["content"] == CONTENT
    assert ("POST", f"{FUNCTION_PATH}/update") in fake.calls
    assert ("POST", "/api/v1/functions/create") not in fake.calls


def test_import_rewrite_is_not_reported_as_drift() -> None:
    """Open WebUI rewrites `from utils` to `from open_webui.utils` before
    storing, so comparing raw source against stored source would push an
    update on every single deploy."""
    source = "from utils.misc import x\n\nclass Filter:\n    pass\n"
    stored = "from open_webui.utils.misc import x\n\nclass Filter:\n    pass\n"
    fake = FakeOwui(state=installed(content=stored))
    original = urllib.request.urlopen
    urllib.request.urlopen = fake
    try:
        installer.ensure_installed(BASE, TOKEN, source)
        installer.verify(BASE, TOKEN, source)
    finally:
        urllib.request.urlopen = original
    writes = [call for call in fake.calls if call[0] != "GET"]
    assert writes == [], f"the import rewrite was treated as drift: {writes}"


def test_verify_rejects_an_install_that_never_became_active() -> None:
    """A 2xx from the install calls is not proof. A filter only runs when it is
    both active and global, so verification must read the end state back."""
    fake = FakeOwui(state=installed(is_active=False, is_global=False))
    original = urllib.request.urlopen
    urllib.request.urlopen = fake
    try:
        try:
            installer.verify(BASE, TOKEN, CONTENT)
        except installer.OwuiError as exc:
            assert "is_active is false" in str(exc)
            assert "is_global is false" in str(exc)
        else:
            raise AssertionError("verify accepted an inactive, non-global filter")
    finally:
        urllib.request.urlopen = original


def test_verify_rejects_a_function_that_vanished() -> None:
    fake = FakeOwui(state=None)
    original = urllib.request.urlopen
    urllib.request.urlopen = fake
    try:
        try:
            installer.verify(BASE, TOKEN, CONTENT)
        except installer.OwuiError as exc:
            assert "still not installed" in str(exc)
        else:
            raise AssertionError("verify accepted a missing filter")
    finally:
        urllib.request.urlopen = original


def test_a_forbidden_read_is_not_mistaken_for_absence() -> None:
    """403 means the token is not an admin. Treating it as "not installed yet"
    would create nothing, install nothing, and report success."""
    fake = FakeOwui(state=installed())
    original = urllib.request.urlopen
    urllib.request.urlopen = fake
    try:
        try:
            installer.read_function(BASE, "not-the-admin-token")
        except installer.OwuiError as exc:
            assert "403" in str(exc)
        else:
            raise AssertionError("a 403 was read as absence")
    finally:
        urllib.request.urlopen = original


def test_deploy_workflow_installs_the_function() -> None:
    """#556's actual complaint: the install exists but no deploy path runs it."""
    workflow = (
        REPO_ROOT / ".github" / "workflows" / "deploy-demo-box.yml"
    ).read_text(encoding="utf-8")
    assert "scripts/owui-mint-admin-token.py" in workflow, (
        "deploy-demo-box.yml does not mint an Open WebUI admin session"
    )
    assert "scripts/install-owui-jwt-forward.py" in workflow, (
        "deploy-demo-box.yml does not install the hive_jwt_forward Function, "
        "so a fresh deploy still has no per-user chat attribution (#556)"
    )


def test_the_source_the_installer_ships_is_a_filter() -> None:
    """Open WebUI decides a Function's type by class name at exec time. A
    class named anything but Filter is never picked up as one, which is how
    this file went unused for months (see its own docstring)."""
    source = installer.DEFAULT_SOURCE.read_text(encoding="utf-8")
    assert "class Filter:" in source
    assert "upstream_auth" in source


def main() -> int:
    tests = [
        value
        for name, value in sorted(globals().items())
        if name.startswith("test_") and callable(value)
    ]
    for test in tests:
        test()
        print(f"ok  {test.__name__}")
    print(f"\n{len(tests)} checks passed")
    return 0


if __name__ == "__main__":
    sys.exit(main())

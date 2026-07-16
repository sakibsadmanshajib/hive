"""
Minimal, dependency-free regression test for hive_agent_console_action.py.

No pytest, no real aiohttp/pydantic install required (repo has no Python
test infra for deploy/docker/pipelines/*.py, and declares neither as a
dependency outside the vendored OpenHands SDK -- see that file's "aiohttp
availability" discovery note). Runs with the stdlib alone:

    python3 deploy/docker/pipelines/test_hive_agent_console_action.py

Fake `aiohttp` and `pydantic` modules are injected into sys.modules
*before* importing the module under test. This is the point: if
hive_agent_console_action.py is missing its `import aiohttp` statement (the
exact CRITICAL bug this test was written for -- the module referenced
aiohttp.ClientSession/ClientTimeout/ClientError without importing the
package, so every button click raised NameError), Python's name resolution
fails on the bare `aiohttp` reference regardless of what's sitting in
sys.modules, and this test fails with that same NameError. A correct
`import aiohttp` picks up the fake from the module cache and the real code
path runs against it.
"""

from __future__ import annotations

import asyncio
import sys
import types
import unittest


class _FakeResponse:
    def __init__(self, status: int, payload: object) -> None:
        self.status = status
        self._payload = payload

    async def json(self) -> object:
        return self._payload

    async def __aenter__(self) -> "_FakeResponse":
        return self

    async def __aexit__(self, *exc: object) -> None:
        return None


class _FakeClientError(Exception):
    pass


def _install_fakes() -> None:
    aiohttp_module = types.ModuleType("aiohttp")

    class ClientTimeout:
        def __init__(self, total: float | None = None) -> None:
            self.total = total

    class ClientSession:
        # Set by each test before use.
        next_response: "_FakeResponse | Exception | None" = None

        async def __aenter__(self) -> "ClientSession":
            return self

        async def __aexit__(self, *exc: object) -> None:
            return None

        def get(self, url: str, headers: dict[str, str], timeout: ClientTimeout):
            resp = ClientSession.next_response
            if isinstance(resp, Exception):
                raise resp
            assert resp is not None, "test must set ClientSession.next_response"
            return resp

    aiohttp_module.ClientError = _FakeClientError  # type: ignore[attr-defined]
    aiohttp_module.ClientTimeout = ClientTimeout  # type: ignore[attr-defined]
    aiohttp_module.ClientSession = ClientSession  # type: ignore[attr-defined]
    sys.modules["aiohttp"] = aiohttp_module

    pydantic_module = types.ModuleType("pydantic")

    class BaseModel:
        # Structural stand-in: reads class-level annotated defaults, same
        # shape hive_agent_console_action.Action.Valves relies on (plain
        # str defaults, no validation). Real pydantic does much more; this
        # test only needs the no-arg-instantiation-with-defaults behavior.
        def __init__(self, **kwargs: object) -> None:
            for name in getattr(type(self), "__annotations__", {}):
                setattr(self, name, kwargs.get(name, getattr(type(self), name, None)))

    pydantic_module.BaseModel = BaseModel  # type: ignore[attr-defined]
    sys.modules["pydantic"] = pydantic_module


_install_fakes()

# Import AFTER the fakes are installed, exactly once module-global (mirrors
# how OWUI's Functions loader imports a Function module once).
import hive_agent_console_action as hcca  # noqa: E402
from aiohttp import ClientSession  # noqa: E402  (the fake installed above)


def _run(coro):
    return asyncio.run(coro)


class CoworkEnabledTests(unittest.TestCase):
    def setUp(self) -> None:
        self.action = hcca.Action()

    def test_true_when_gate_on(self) -> None:
        ClientSession.next_response = _FakeResponse(200, {"gates": {"ENABLE_COWORK": True}})
        self.assertTrue(_run(self.action._cowork_enabled("tok")))

    def test_false_when_gate_off(self) -> None:
        ClientSession.next_response = _FakeResponse(200, {"gates": {"ENABLE_COWORK": False}})
        self.assertFalse(_run(self.action._cowork_enabled("tok")))

    def test_false_on_non_200(self) -> None:
        ClientSession.next_response = _FakeResponse(500, {})
        self.assertFalse(_run(self.action._cowork_enabled("tok")))

    def test_false_on_client_error(self) -> None:
        ClientSession.next_response = _FakeClientError("boom")
        self.assertFalse(_run(self.action._cowork_enabled("tok")))

    def test_false_on_malformed_payload(self) -> None:
        ClientSession.next_response = _FakeResponse(200, {"gates": "not-a-dict"})
        self.assertFalse(_run(self.action._cowork_enabled("tok")))


class ActionMessageTests(unittest.TestCase):
    def test_no_session_shows_error_and_never_calls_gate(self) -> None:
        action = hcca.Action()
        emitted: list[dict] = []

        async def emit(event: dict) -> None:
            emitted.append(event)

        _run(action.action({}, __oauth_token__=None, __event_emitter__=emit))
        self.assertEqual(len(emitted), 1)
        self.assertIn("no active Hive session", emitted[0]["data"]["content"])

    def test_gate_disabled_shows_error_not_link(self) -> None:
        action = hcca.Action()
        ClientSession.next_response = _FakeResponse(200, {"gates": {"ENABLE_COWORK": False}})
        emitted: list[dict] = []

        async def emit(event: dict) -> None:
            emitted.append(event)

        _run(
            action.action(
                {}, __oauth_token__={"access_token": "tok"}, __event_emitter__=emit
            )
        )
        self.assertEqual(len(emitted), 1)
        self.assertIn("not enabled", emitted[0]["data"]["content"])

    def test_gate_enabled_emits_console_link(self) -> None:
        action = hcca.Action()
        ClientSession.next_response = _FakeResponse(200, {"gates": {"ENABLE_COWORK": True}})
        emitted: list[dict] = []

        async def emit(event: dict) -> None:
            emitted.append(event)

        _run(
            action.action(
                {}, __oauth_token__={"access_token": "tok"}, __event_emitter__=emit
            )
        )
        self.assertEqual(len(emitted), 1)
        self.assertIn(action.valves.console_path, emitted[0]["data"]["content"])


if __name__ == "__main__":
    unittest.main()

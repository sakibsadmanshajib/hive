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


class _Recorder:
    """Collects emitted events and answers __event_call__ with a set result."""

    def __init__(self, call_result: object = "new-tab") -> None:
        self.emitted: list[dict] = []
        self.calls: list[dict] = []
        self.call_result = call_result

    async def emit(self, event: dict) -> None:
        self.emitted.append(event)

    async def call(self, event: dict) -> object:
        self.calls.append(event)
        return self.call_result

    @property
    def types(self) -> list[str]:
        return [event.get("type") for event in self.emitted]


class TranscriptCleanlinessTests(unittest.TestCase):
    """#541: nothing this Action does may become permanent chat content.

    The regression these guard is the previous implementation, which emitted
    `type: "message"` events on all three paths. Those render as assistant
    content and are saved with the conversation, so both failure messages and
    the launcher link permanently polluted the user's history.
    """

    def test_no_path_emits_a_message_event(self) -> None:
        for label, gate, token, call_result in (
            ("no session", None, None, "new-tab"),
            ("gate off", {"gates": {"ENABLE_COWORK": False}}, "tok", "new-tab"),
            ("gate on", {"gates": {"ENABLE_COWORK": True}}, "tok", "new-tab"),
            ("navigation failed", {"gates": {"ENABLE_COWORK": True}}, "tok", None),
        ):
            with self.subTest(label):
                action = hcca.Action()
                if gate is not None:
                    ClientSession.next_response = _FakeResponse(200, gate)
                rec = _Recorder(call_result)
                oauth = {"access_token": token} if token else None

                result = _run(
                    action.action(
                        {},
                        __oauth_token__=oauth,
                        __event_emitter__=rec.emit,
                        __event_call__=rec.call,
                    )
                )

                # A returned {"messages": [...]} is the other way to write to
                # the transcript, so the return value is asserted too.
                self.assertIsNone(result)
                self.assertNotIn("message", rec.types)
                self.assertNotIn("replace", rec.types)

    def test_failures_are_notifications(self) -> None:
        for label, gate, token, expected in (
            ("no session", None, None, "no active Hive session"),
            ("gate off", {"gates": {"ENABLE_COWORK": False}}, "tok", "not enabled"),
        ):
            with self.subTest(label):
                action = hcca.Action()
                if gate is not None:
                    ClientSession.next_response = _FakeResponse(200, gate)
                rec = _Recorder()
                oauth = {"access_token": token} if token else None

                _run(
                    action.action(
                        {},
                        __oauth_token__=oauth,
                        __event_emitter__=rec.emit,
                        __event_call__=rec.call,
                    )
                )

                self.assertEqual(rec.types, ["notification"])
                self.assertEqual(rec.emitted[0]["data"]["type"], "error")
                self.assertIn(expected, rec.emitted[0]["data"]["content"])
                # A failed gate must never reach the navigation step.
                self.assertEqual(rec.calls, [])


class NavigationTests(unittest.TestCase):
    def _open(self, call_result: object = "new-tab") -> _Recorder:
        action = hcca.Action()
        ClientSession.next_response = _FakeResponse(200, {"gates": {"ENABLE_COWORK": True}})
        rec = _Recorder(call_result)
        _run(
            action.action(
                {},
                __oauth_token__={"access_token": "tok"},
                __event_emitter__=rec.emit,
                __event_call__=rec.call,
            )
        )
        return rec

    def test_navigates_via_execute_event(self) -> None:
        rec = self._open()
        self.assertEqual(len(rec.calls), 1)
        self.assertEqual(rec.calls[0]["type"], "execute")
        code = rec.calls[0]["data"]["code"]
        self.assertIn("window.open", code)
        self.assertIn(hcca.Action().valves.console_path, code)
        # Success is silent: no toast on a click that worked.
        self.assertEqual(rec.emitted, [])

    def test_same_tab_fallback_counts_as_success(self) -> None:
        # A blocked popup navigates in place; the user arrived, so no error.
        self.assertEqual(self._open("same-tab").emitted, [])

    def test_unexpected_result_surfaces_a_toast(self) -> None:
        rec = self._open(None)
        self.assertEqual(rec.types, ["notification"])
        self.assertEqual(rec.emitted[0]["data"]["type"], "error")

    def test_url_is_json_encoded_into_the_snippet(self) -> None:
        # The snippet is evaluated as JavaScript source by Open WebUI, so a
        # valve value carrying a quote must not be able to close the string
        # literal and run as code.
        action = hcca.Action()
        action.valves.console_path = '/x";alert(1);//'
        ClientSession.next_response = _FakeResponse(200, {"gates": {"ENABLE_COWORK": True}})
        rec = _Recorder()
        _run(
            action.action(
                {},
                __oauth_token__={"access_token": "tok"},
                __event_emitter__=rec.emit,
                __event_call__=rec.call,
            )
        )
        code = rec.calls[0]["data"]["code"]
        self.assertIn(r"\"", code)
        self.assertNotIn('const url = "/x";alert(1);//"', code)

    def test_missing_event_call_degrades_to_a_toast(self) -> None:
        action = hcca.Action()
        ClientSession.next_response = _FakeResponse(200, {"gates": {"ENABLE_COWORK": True}})
        rec = _Recorder()
        _run(
            action.action(
                {},
                __oauth_token__={"access_token": "tok"},
                __event_emitter__=rec.emit,
                __event_call__=None,
            )
        )
        self.assertEqual(rec.types, ["notification"])
        self.assertIn(action.valves.console_path, rec.emitted[0]["data"]["content"])

    def test_declares_event_call_so_open_webui_injects_it(self) -> None:
        # Open WebUI copies __event_call__ into the kwargs only when the
        # parameter is declared in the signature. Drop it and the button
        # silently stops navigating.
        import inspect

        params = inspect.signature(hcca.Action.action).parameters
        self.assertIn("__event_call__", params)
        self.assertIn("__event_emitter__", params)


if __name__ == "__main__":
    unittest.main()

#!/usr/bin/env python3
"""Self-checks for deploy/docker/owui-patches/hive_agent_proxy.py.

Pure standard library, no test framework and no network, matching the other
scripts/test_owui_*.py self-checks so `make test-scripts` can run it on a plain
ubuntu-latest runner. FastAPI, aiohttp and open_webui only exist inside the
chat image, so they are stubbed here: the module under test is imported against
those stubs and its real logic runs unchanged.

What is being protected. This proxy is the only thing standing between a
signed-in chat user and a gateway credential, so the properties worth a test
are the ones whose failure is silent:

  * the shim key goes on Authorization and the user's token goes on the carrier
    header, never the other way round and never both on one header;
  * a request with no resolvable user token is refused before any upstream call,
    rather than proceeding under the shim's own principal;
  * neither credential is ever returned to the caller;
  * the caller cannot choose the principal, and cannot smuggle extra fields
    into the upstream body;
  * a task id that is not a UUID never reaches a URL.
"""

from __future__ import annotations

import asyncio
import json
import pathlib
import sys
import types
from uuid import uuid4

REPO = pathlib.Path(__file__).resolve().parent.parent
MODULE = REPO / 'deploy' / 'docker' / 'owui-patches' / 'hive_agent_proxy.py'

failures: list[str] = []


def check(condition: bool, message: str) -> None:
    if not condition:
        failures.append(message)


# ---------------------------------------------------------------- stub layer


class HTTPException(Exception):
    def __init__(self, status_code: int, detail: str = '') -> None:
        super().__init__(detail)
        self.status_code = status_code
        self.detail = detail


class JSONResponse:
    def __init__(self, status_code: int = 200, content=None) -> None:
        self.status_code = status_code
        self.content = content


class APIRouter:
    def __init__(self) -> None:
        self.routes: dict[tuple[str, str], object] = {}

    def _register(self, method: str, path: str):
        def decorator(fn):
            self.routes[(method, path)] = fn
            return fn

        return decorator

    def get(self, path: str):
        return self._register('GET', path)

    def post(self, path: str):
        return self._register('POST', path)


def Depends(dependency):  # noqa: N802 - mirrors FastAPI's own name
    return dependency


class Request:
    """Only the two surfaces the module touches."""

    def __init__(self, body: bytes = b'') -> None:
        self._body = body

    async def body(self) -> bytes:
        return self._body

    async def json(self):
        return json.loads(self._body.decode())


class RecordingSession:
    """Captures one upstream call instead of making it."""

    calls: list[dict] = []
    status = 200
    payload: object = {'tasks': []}

    def __init__(self, *_args, **_kwargs) -> None:
        pass

    async def __aenter__(self):
        return self

    async def __aexit__(self, *_exc):
        return False

    def request(self, method, url, headers=None, json=None):  # noqa: A002
        RecordingSession.calls.append(
            {'method': method, 'url': url, 'headers': dict(headers or {}), 'json': json}
        )
        return self

    async def json(self, content_type=None):  # noqa: ARG002
        return RecordingSession.payload

def install_stubs() -> None:
    fastapi = types.ModuleType('fastapi')
    fastapi.APIRouter = APIRouter
    fastapi.Depends = Depends
    fastapi.HTTPException = HTTPException
    fastapi.Request = Request
    responses = types.ModuleType('fastapi.responses')
    responses.JSONResponse = JSONResponse
    fastapi.responses = responses
    sys.modules['fastapi'] = fastapi
    sys.modules['fastapi.responses'] = responses

    aiohttp = types.ModuleType('aiohttp')

    class ClientTimeout:
        def __init__(self, total=None) -> None:
            self.total = total

    class ClientError(Exception):
        pass

    aiohttp.ClientTimeout = ClientTimeout
    aiohttp.ClientError = ClientError
    aiohttp.ClientSession = RecordingSession
    sys.modules['aiohttp'] = aiohttp

    open_webui = types.ModuleType('open_webui')
    utils = types.ModuleType('open_webui.utils')
    auth = types.ModuleType('open_webui.utils.auth')

    def get_verified_user():
        raise AssertionError('the dependency must never be called directly in these checks')

    auth.get_verified_user = get_verified_user
    middleware = types.ModuleType('open_webui.utils.middleware')

    async def get_system_oauth_token(_request, _user):
        return {'access_token': TOKEN_HOLDER['token']} if TOKEN_HOLDER['token'] else None

    middleware.get_system_oauth_token = get_system_oauth_token
    open_webui.utils = utils
    utils.auth = auth
    utils.middleware = middleware
    sys.modules['open_webui'] = open_webui
    sys.modules['open_webui.utils'] = utils
    sys.modules['open_webui.utils.auth'] = auth
    sys.modules['open_webui.utils.middleware'] = middleware


TOKEN_HOLDER = {'token': 'user-supabase-token'}

install_stubs()

import importlib.util  # noqa: E402

# MODULE_OVERRIDE lets a caller point these checks at a mutated copy, which is
# how the self-check proves it can go red rather than only green.
SOURCE = pathlib.Path(sys.argv[1]) if len(sys.argv) > 1 else MODULE
_spec = importlib.util.spec_from_file_location('hive_agent_proxy', SOURCE)
assert _spec is not None and _spec.loader is not None
proxy = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(proxy)


# ------------------------------------------------------------------ fixtures

import os  # noqa: E402

os.environ['OPENAI_API_BASE_URL'] = 'http://edge-api:8080/v1'
os.environ['OPENAI_API_KEY'] = 'hk_shim_key_for_tests'

USER = types.SimpleNamespace(id='11111111-1111-4111-8111-111111111111')


def run(coro):
    return asyncio.run(coro)


def reset() -> None:
    RecordingSession.calls = []
    RecordingSession.status = 200
    RecordingSession.payload = {'tasks': []}
    TOKEN_HOLDER['token'] = 'user-supabase-token'


# -------------------------------------------------------------------- checks


def check_credentials_land_on_the_right_headers() -> None:
    reset()
    run(proxy.list_tasks(Request(), USER))

    check(len(RecordingSession.calls) == 1, 'list must make exactly one upstream call')
    call = RecordingSession.calls[0]
    headers = call['headers']

    check(
        headers.get('Authorization') == 'Bearer hk_shim_key_for_tests',
        'the shim key must be the Authorization credential, since that is what '
        'grants edge-api the tenant fallback',
    )
    check(
        headers.get(proxy.UPSTREAM_AUTH_HEADER) == 'Bearer user-supabase-token',
        "the signed-in user's token must ride on the carrier header",
    )
    check(
        'user-supabase-token' not in headers.get('Authorization', ''),
        'the user token must never be the Authorization credential here',
    )
    check(
        call['url'] == 'http://edge-api:8080/v1/agent/tasks',
        f"unexpected upstream url {call['url']}",
    )
    check(call['method'] == 'GET', 'list must be a GET')


def check_no_user_token_is_refused_before_any_upstream_call() -> None:
    reset()
    TOKEN_HOLDER['token'] = ''
    try:
        run(proxy.list_tasks(Request(), USER))
    except HTTPException as exc:
        check(exc.status_code == 401, f'expected 401 with no user token, got {exc.status_code}')
        check(
            'hk_shim_key_for_tests' not in str(exc.detail),
            'the refusal must not leak the shim key',
        )
    else:
        failures.append('a request with no user token must be refused, not forwarded')
    check(
        RecordingSession.calls == [],
        'no upstream call may be made once the user token is missing',
    )


def check_create_forwards_only_the_two_named_fields() -> None:
    reset()
    body = json.dumps(
        {
            'pack': 'knowledge-work-pack',
            'instructions': '  Summarise the policy  ',
            # Everything below is what a caller would try. None of it may survive.
            '__metadata': {'upstream_auth': 'Bearer attacker-token'},
            'user_id': '22222222-2222-4222-8222-222222222222',
            'tenant_id': '33333333-3333-4333-8333-333333333333',
        }
    ).encode()
    RecordingSession.payload = {'id': str(uuid4())}

    run(proxy.create_task(Request(body), USER))

    forwarded = RecordingSession.calls[0]['json']
    check(
        forwarded == {'pack': 'knowledge-work-pack', 'instructions': 'Summarise the policy'},
        f'create forwarded {forwarded}, which is not exactly the two named fields',
    )


def check_empty_create_fields_are_refused() -> None:
    for body in (
        {'pack': '', 'instructions': 'do a thing'},
        {'pack': 'coding-pack', 'instructions': '   '},
        {'pack': 'coding-pack'},
        {'instructions': 'do a thing'},
        ['not', 'an', 'object'],
    ):
        reset()
        try:
            run(proxy.create_task(Request(json.dumps(body).encode()), USER))
        except HTTPException as exc:
            check(exc.status_code == 400, f'expected 400 for {body}, got {exc.status_code}')
        else:
            failures.append(f'create must refuse {body}')
        check(RecordingSession.calls == [], f'no upstream call may be made for {body}')


def check_task_id_must_be_a_uuid() -> None:
    for bad in ('../../admin', 'not-a-uuid', '', '1 OR 1=1'):
        reset()
        try:
            run(proxy.get_task(bad, Request(), USER))
        except HTTPException as exc:
            check(exc.status_code == 400, f'expected 400 for task id {bad!r}')
        else:
            failures.append(f'a task id of {bad!r} must never reach a URL')
        check(RecordingSession.calls == [], f'no upstream call may be made for task id {bad!r}')

    reset()
    good = str(uuid4())
    RecordingSession.payload = {'id': good}
    run(proxy.cancel_task(good, Request(), USER))
    check(
        RecordingSession.calls[0]['url'] == f'http://edge-api:8080/v1/agent/tasks/{good}/cancel',
        'a valid uuid must reach the cancel path unchanged',
    )


def check_upstream_body_is_returned_verbatim() -> None:
    reset()
    RecordingSession.payload = {'error': {'message': 'Cowork is not enabled for this tenant.'}}
    # aiohttp reports the upstream status on `.status`, and the module must
    # pass it through rather than flattening every answer to 200.
    RecordingSession.status = 403

    response = run(proxy.list_tasks(Request(), USER))
    check(isinstance(response, JSONResponse), 'the proxy must answer with the upstream JSON')
    check(
        response.content == {'error': {'message': 'Cowork is not enabled for this tenant.'}},
        "edge-api's own error sentence must survive to the browser",
    )


def check_no_gateway_configuration_is_a_stated_503() -> None:
    reset()
    saved = os.environ.pop('OPENAI_API_KEY')
    try:
        run(proxy.list_tasks(Request(), USER))
    except HTTPException as exc:
        check(exc.status_code == 503, f'expected 503 with no shim key, got {exc.status_code}')
    else:
        failures.append('a deployment with no gateway credential must say so, not proceed')
    finally:
        os.environ['OPENAI_API_KEY'] = saved
    check(RecordingSession.calls == [], 'no upstream call may be made with no shim key')


def check_every_route_is_behind_the_session_dependency() -> None:
    source = SOURCE.read_text()
    routes = [
        ("@router.get('/tasks')", 'async def list_tasks'),
        ("@router.post('/tasks')", 'async def create_task'),
        ("@router.get('/tasks/{task_id}')", 'async def get_task'),
        ("@router.post('/tasks/{task_id}/cancel')", 'async def cancel_task'),
    ]
    for decorator, definition in routes:
        check(decorator in source, f'missing route {decorator}')
        start = source.index(definition)
        signature = source[start : source.index(':\n', start)]
        check(
            'Depends(get_verified_user)' in signature,
            f'{definition} must resolve its principal from the session, not the request',
        )
    # The inverse of the same rule: no route may read an identity off the wire.
    for smell in ('user_id', 'tenant_id', "headers.get('Authorization')"):
        check(
            f"request.query_params.get('{smell}')" not in source
            and f"submitted.get('{smell}')" not in source,
            f'the proxy must never read {smell} from the caller',
        )


for check_fn in (
    check_credentials_land_on_the_right_headers,
    check_no_user_token_is_refused_before_any_upstream_call,
    check_create_forwards_only_the_two_named_fields,
    check_empty_create_fields_are_refused,
    check_task_id_must_be_a_uuid,
    check_upstream_body_is_returned_verbatim,
    check_no_gateway_configuration_is_a_stated_503,
    check_every_route_is_behind_the_session_dependency,
):
    check_fn()

if failures:
    print('hive_agent_proxy self-check FAILED:', file=sys.stderr)
    for failure in failures:
        print(f'  - {failure}', file=sys.stderr)
    raise SystemExit(1)

print('hive_agent_proxy self-check: all checks passed')

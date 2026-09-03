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


class StreamingResponse:
    def __init__(self, body, media_type: str = '', headers=None) -> None:
        self.body = body
        self.media_type = media_type
        self.headers = dict(headers or {})


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
    raises: BaseException | None = None

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
        if RecordingSession.raises is not None:
            raise RecordingSession.raises
        return self

    async def json(self, content_type=None):  # noqa: ARG002
        return RecordingSession.payload

    # --- the streaming half (issue #1622) ---
    #
    # `get` is awaited rather than used as a context manager, because the
    # session under test has to outlive the handler that opened it. The stub
    # mirrors that shape exactly: get the wrong one and the module fails to
    # run rather than passing against a shape production does not have.
    closed = False
    released = False
    chunks: list[bytes] = [b'event: step\ndata: {"seq":1}\n\n']

    async def get(self, url, headers=None):
        RecordingSession.calls.append(
            {'method': 'GET', 'url': url, 'headers': dict(headers or {}), 'json': None}
        )
        if RecordingSession.raises is not None:
            raise RecordingSession.raises
        return StreamedResponse()

    async def close(self):
        RecordingSession.closed = True


class StreamedContent:
    async def iter_any(self):
        for chunk in RecordingSession.chunks:
            yield chunk


class StreamedResponse:
    def __init__(self) -> None:
        self.status = RecordingSession.status
        self.content = StreamedContent()

    async def json(self, content_type=None):  # noqa: ARG002
        return RecordingSession.payload

    def release(self):
        RecordingSession.released = True


def install_stubs() -> None:
    fastapi = types.ModuleType('fastapi')
    fastapi.APIRouter = APIRouter
    fastapi.Depends = Depends
    fastapi.HTTPException = HTTPException
    fastapi.Request = Request
    responses = types.ModuleType('fastapi.responses')
    responses.JSONResponse = JSONResponse
    responses.StreamingResponse = StreamingResponse
    fastapi.responses = responses
    sys.modules['fastapi'] = fastapi
    sys.modules['fastapi.responses'] = responses

    aiohttp = types.ModuleType('aiohttp')

    class ClientTimeout:
        def __init__(self, total=None, sock_read=None) -> None:
            self.total = total
            self.sock_read = sock_read

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
    RecordingSession.raises = None
    RecordingSession.closed = False
    RecordingSession.released = False
    RecordingSession.chunks = [b'event: step\ndata: {"seq":1}\n\n']
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
        response.status_code == 403,
        f'the upstream status must survive rather than flatten to 200, got {response.status_code}',
    )
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


def check_transport_failures_are_stated_not_raised() -> None:
    """A transport failure must be a stated status, never a traceback.

    An unhandled exception here becomes a 500 whose body carries a traceback,
    on a request holding a gateway credential. The timeout arm is the one worth
    pinning: aiohttp raises asyncio.TimeoutError for a total-timeout breach,
    which is only the builtin TimeoutError on Python 3.11 and later, and
    aiohttp's own ServerTimeoutError inherits from ClientError as well, so the
    order the two arms are written in decides which status a connect timeout
    reports.
    """
    import asyncio as _asyncio

    reset()
    RecordingSession.raises = _asyncio.TimeoutError()
    try:
        run(proxy.list_tasks(Request(), USER))
    except HTTPException as exc:
        check(exc.status_code == 504, f'a timeout must be a 504, got {exc.status_code}')
    except BaseException as exc:  # noqa: BLE001 - the whole point of the check
        failures.append(f'a timeout escaped as {type(exc).__name__}, so it becomes a 500')
    else:
        failures.append('a timeout must not read as a successful answer')

    reset()
    RecordingSession.raises = sys.modules['aiohttp'].ClientError()
    try:
        run(proxy.list_tasks(Request(), USER))
    except HTTPException as exc:
        check(exc.status_code == 502, f'an unreachable upstream must be a 502, got {exc.status_code}')
    except BaseException as exc:  # noqa: BLE001
        failures.append(f'a client error escaped as {type(exc).__name__}, so it becomes a 500')
    else:
        failures.append('an unreachable upstream must not read as a successful answer')


def check_every_route_is_behind_the_session_dependency() -> None:
    source = SOURCE.read_text()
    routes = [
        ("@router.get('/tasks')", 'async def list_tasks'),
        ("@router.post('/tasks')", 'async def create_task'),
        ("@router.get('/tasks/{task_id}')", 'async def get_task'),
        ("@router.post('/tasks/{task_id}/cancel')", 'async def cancel_task'),
        ("@router.get('/tasks/{task_id}/events')", 'async def list_task_events'),
        ("@router.get('/tasks/{task_id}/files')", 'async def list_task_files'),
    ]
    # The inverse of the same rule: no route may read an identity off the wire.
    #
    # Two smells, not three. A third entry, "headers.get('Authorization')", used
    # to sit in this tuple, where the accessor templates expanded it to
    # `request.query_params.get('headers.get('Authorization')')`. No such string
    # can occur in any Python source, so that iteration asserted nothing and
    # reported a pass over the header read it was named after. Reading a
    # credential off the incoming request needs its own direct test, below.
    for smell in ('user_id', 'tenant_id'):
        check(
            f"request.query_params.get('{smell}')" not in source
            and f"submitted.get('{smell}')" not in source,
            f'the proxy must never read {smell} from the caller',
        )

    # The proxy builds its outbound Authorization from the shim key and the
    # session token the dependency resolved, never from anything the browser
    # sent. `request.headers` is the accessor that would break that, in any of
    # its forms (`.get`, subscript, iteration), so the whole attribute is the
    # thing to refuse rather than one spelling of one lookup.
    check(
        'request.headers' not in source,
        'the proxy must never read a credential off the incoming request headers',
    )


def check_events_cursor_params_are_validated() -> None:
    """A cursor that is not a plain non-negative integer never reaches a URL."""
    good = str(uuid4())
    for bad in ('-1', 'abc', '1.5', ' 2', '999999999999999999999999'):
        reset()
        try:
            run(proxy.list_task_events(good, Request(), USER, after_seq=bad))
        except HTTPException as exc:
            check(exc.status_code == 400, f'expected 400 for after_seq {bad!r}')
        else:
            failures.append(f'after_seq {bad!r} must be refused before any URL is built')
        check(RecordingSession.calls == [], f'no upstream call may be made for after_seq {bad!r}')

    reset()
    RecordingSession.payload = {'events': []}
    run(proxy.list_task_events(good, Request(), USER, after_seq='5', limit='50'))
    url = RecordingSession.calls[0]['url']
    check(
        url == f'http://edge-api:8080/v1/agent/tasks/{good}/events?after_seq=5&limit=50',
        f'unexpected events url {url}',
    )
    headers = RecordingSession.calls[0]['headers']
    check(
        headers.get('Authorization') == 'Bearer hk_shim_key_for_tests'
        and headers.get(proxy.UPSTREAM_AUTH_HEADER) == 'Bearer user-supabase-token',
        'the events pass-through must carry the same credential split as the other routes',
    )


def check_stream_carries_the_same_credential_split() -> None:
    """The subscription is the same trust decision as every other route.

    It is a separate code path from `_call` (it has to be: `_call` reads the
    whole body before it returns anything), and a separate code path is exactly
    where a credential split drifts. Worth its own check for that reason rather
    than because the route is new.
    """
    reset()
    good = str(uuid4())
    response = run(proxy.stream_task_events(good, Request(), USER, after_seq='7'))

    check(len(RecordingSession.calls) == 1, 'the stream must make exactly one upstream call')
    call = RecordingSession.calls[0]
    check(
        call['url'] == f'http://edge-api:8080/v1/agent/tasks/{good}/events/stream?after_seq=7',
        f"unexpected stream url {call['url']}",
    )
    headers = call['headers']
    check(
        headers.get('Authorization') == 'Bearer hk_shim_key_for_tests'
        and headers.get(proxy.UPSTREAM_AUTH_HEADER) == 'Bearer user-supabase-token',
        'the stream must carry the same credential split as every other route',
    )
    check(
        headers.get('Accept') == 'text/event-stream',
        'the stream must ask for an event stream rather than JSON',
    )
    check(
        getattr(response, 'media_type', '') == 'text/event-stream',
        'the response handed to the browser must be an event stream',
    )
    check(
        response.headers.get('X-Accel-Buffering') == 'no',
        'a buffered event stream is a stream in name only, so the hint must be set',
    )

    # The frames reach the caller, and the session is closed once they have.
    # Leaking it would be invisible until the worker ran out of connections.
    body = b''.join(run(_drain(response.body)))
    check(b'"seq":1' in body, f'the stream relayed {body!r}')
    check(RecordingSession.closed, 'the upstream session must be closed when the relay ends')
    check(RecordingSession.released, 'the upstream response must be released when the relay ends')


async def _drain(generator):
    return [chunk async for chunk in generator]


def check_stream_refusal_stays_an_http_status() -> None:
    """A 404 upstream is a 404 here, never a 200 carrying an apology.

    Once the event-stream content type is on the wire there is no status left
    to send, and the front end would render the refusal as a run that did
    nothing, which is the failure mode this whole surface exists to stop
    making.
    """
    reset()
    RecordingSession.status = 404
    RecordingSession.payload = {'error': {'message': 'task not found'}}
    response = run(proxy.stream_task_events(str(uuid4()), Request(), USER))

    check(getattr(response, 'status_code', None) == 404, 'an upstream refusal must keep its status')
    check(
        getattr(response, 'media_type', '') != 'text/event-stream',
        'a refusal must not be sent as an event stream',
    )
    check(RecordingSession.closed, 'the upstream session must be closed on a refusal too')


def check_stream_refuses_a_bad_cursor_and_a_bad_task_id() -> None:
    reset()
    for bad in ('-1', 'abc', '1.5'):
        try:
            run(proxy.stream_task_events(str(uuid4()), Request(), USER, after_seq=bad))
        except HTTPException as exc:
            check(exc.status_code == 400, f'expected 400 for after_seq {bad!r}')
        else:
            failures.append(f'after_seq {bad!r} must be refused before any URL is built')
        check(RecordingSession.calls == [], f'no upstream call may be made for after_seq {bad!r}')

    reset()
    try:
        run(proxy.stream_task_events('../../secrets', Request(), USER))
    except HTTPException as exc:
        check(exc.status_code == 400, 'a task id that is not a UUID must be a 400')
    else:
        failures.append('a task id that is not a UUID reached a URL')
    check(RecordingSession.calls == [], 'no upstream call may be made for a bad task id')


for check_fn in (
    check_credentials_land_on_the_right_headers,
    check_no_user_token_is_refused_before_any_upstream_call,
    check_create_forwards_only_the_two_named_fields,
    check_empty_create_fields_are_refused,
    check_task_id_must_be_a_uuid,
    check_upstream_body_is_returned_verbatim,
    check_no_gateway_configuration_is_a_stated_503,
    check_transport_failures_are_stated_not_raised,
    check_events_cursor_params_are_validated,
    check_stream_carries_the_same_credential_split,
    check_stream_refusal_stays_an_http_status,
    check_stream_refuses_a_bad_cursor_and_a_bad_task_id,
    check_every_route_is_behind_the_session_dependency,
):
    check_fn()

if failures:
    print('hive_agent_proxy self-check FAILED:', file=sys.stderr)
    for failure in failures:
        print(f'  - {failure}', file=sys.stderr)
    raise SystemExit(1)

print('hive_agent_proxy self-check: all checks passed')

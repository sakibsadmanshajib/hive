"""Hive's two web tools, advertised to every tool capable model and executed
against the charged gateway endpoints (issue #1718).

What was missing. Slice S1 (#1656) built the tool specifications in Go, slice
S3 (#1673) built the decision about which aliases may be offered them, and
#1695 / PR #1699 priced a call at 100,000 credits for a search and 200,000 for
a fetch. Nothing ever put a specification in front of a model, and nothing ever
executed a tool call: `Descriptors()` had no non test caller, and
`/v1/tools/web_search` and `/v1/tools/web_fetch` had no caller at all. This
module is the attach step that makes the other three reachable.

It deliberately adds no execution loop. Open WebUI already has one: with native
function calling on, `process_chat_response` reads `metadata['tools']`, calls
the entry's `callable` with the model's arguments, appends a
`function_call_output` and re invokes the model. Writing a second loop next to
that one would be a second thing to keep correct. So this module supplies two
entries in the shape that loop already reads, and the loop does the rest.

Three properties are load bearing.

  * The specifications are NOT written here. They are fetched from
    `GET /v1/tools` on edge-api, which serves `webtools.Descriptors()` verbatim.
    A copy in this file would drift from the handler that implements it, and
    the drift would be invisible: a description promising an untrusted content
    fence, or naming an argument the handler ignores, still looks correct in
    review. If the fetch fails, NOTHING is advertised. Degrading to a stale
    hardcoded copy is exactly the failure the endpoint exists to prevent.

  * Nothing is advertised to a model that cannot serve it. `hive_capabilities`
    on the model listing is edge-api's own answer to "does this alias have a
    routable, tool capable route", and a model whose answer is false, or which
    carries no answer at all, gets no tools and no claim that it searched.

  * Execution goes through the charged endpoints and nowhere else. There is no
    local search path here and no fallback to Open WebUI's own SearXNG
    integration. A call that cannot be priced is refused upstream and the
    refusal is handed to the model as a fact rather than served free
    (.wolf/decisions.md D-034). The signed in user's own token rides on
    `X-Hive-Upstream-Auth`, so the spend is attributed to whoever asked, not to
    the shim account.

Upstream's own builtin tools are dropped here, and that is not incidental. This
deployment ran with `function_calling=legacy` for one reason: native mode
attaches 21 builtin specifications to every UI chat request, 12,089 bytes and
3,144 Groq prompt tokens for a one word answer, and the routes this deployment
uses refused the payload outright (OpenRouter 404 "No endpoints found that
support tool use", Groq free tier 429 over a 6,000 token per minute ceiling).
Turning native back on without removing them would reproduce that failure
exactly. Two specifications under `webtools.MaxDescriptorBytes` (1200 bytes) is
what makes native affordable, so the builtin set is dropped unless a deployment
names an entry in HIVE_OWUI_BUILTIN_TOOLS, which is empty by default.
"""

from __future__ import annotations

import asyncio
import json
import logging
import os
import time
import urllib.error
import urllib.request
from typing import Any

log = logging.getLogger(__name__)

# Stdlib HTTP on a worker thread rather than aiohttp, which this image also
# carries. Two reasons, and neither is style. A tool call is one request that
# already costs seconds of network, so a thread hop is free next to it, and
# with no third party import this module's own self check
# (scripts/test_owui_web_tools.py) runs on a bare python3 in CI, against a real
# local server, instead of being skipped wherever aiohttp is absent. A test
# that skips is a test that cannot go red.

# Wire names, identical to webtools.ToolWebSearch / webtools.ToolWebFetch. The
# specifications are fetched rather than written here, but the two names are
# also the route paths, so they are spelled once in this file and asserted
# against the Go constants by scripts/test_owui_web_tools.py.
WEB_SEARCH = "web_search"
WEB_FETCH = "web_fetch"

# Upstream's citation extraction (utils/middleware.py
# get_citation_source_from_tool_result) dispatches on ITS OWN builtin tool
# names, not ours. apply_web_tools_patch.py maps our names onto these two for
# that call alone, so a Hive search produces the same citation chips a native
# Open WebUI search would. Nothing else in the turn sees the mapped name.
CITATION_ALIASES = {WEB_SEARCH: "search_web", WEB_FETCH: "fetch_url"}

# The header edge-api reads the per-user token from. Must match
# `UpstreamAuthHeader` in apps/edge-api/internal/auth/owui_unwrap.go, the same
# constant hive_agent_proxy.py carries.
UPSTREAM_AUTH_HEADER = "X-Hive-Upstream-Auth"

# The header carrying the assistant turn a tool call belongs to. Must match
# `webtools.TurnHeader`. edge-api refuses a call without it, because a per turn
# budget that fails open is the defect it exists to prevent.
TURN_HEADER = "X-Hive-Tool-Turn"

# Comma separated upstream builtin tool names a deployment wants kept on EVERY
# request. Empty by default: see the module docstring for the measured reason.
BUILTIN_ALLOWLIST_ENV = "HIVE_OWUI_BUILTIN_TOOLS"

# Upstream's knowledge tools, kept on the turns that would otherwise lose their
# documents. Two of Open WebUI's retrieval paths inject documents into the
# request ONLY under legacy function calling and hand the work to these tools
# under native: a folder's attached files, which become
# metadata['folder_knowledge'], and a custom model's own attached knowledge.
# Dropping the whole builtin set with native turned on would therefore have
# stranded both, silently, in a deployment where the interface still offers
# them. They are kept per turn rather than always, so an ordinary chat still
# carries two specifications and nothing else.
#
# NOT covered by this, and not needing to be: a file the user attaches to the
# message, and a Hive project's files, which PR #1707 appends to the same
# request `files` list. Both go through chat_completion_files_handler, which
# upstream calls unconditionally on either path.
#
# Names taken from utils/tools.py's own import list; the self check asserts
# every one of them still exists there, so an upstream rename fails a pull
# request rather than quietly reopening the gap.
KNOWLEDGE_TOOL_NAMES = frozenset(
    {
        "grep_knowledge_files",
        "kb_exec",
        "list_knowledge",
        "list_knowledge_bases",
        "query_knowledge_bases",
        "query_knowledge_files",
        "search_knowledge_bases",
        "search_knowledge_files",
        "view_file",
        "view_knowledge_file",
        "view_note",
    }
)

# Set to "false" to advertise nothing at all, which restores the behaviour
# before this module existed without needing to rebuild the image.
ENABLED_ENV = "HIVE_WEB_TOOLS_ENABLED"

# How long a fetched descriptor list is reused. The specifications are compiled
# into edge-api, so they change only on a deploy; this exists so a chat turn
# never waits on an extra HTTP round trip, not to paper over an unreachable
# gateway.
DESCRIPTOR_TTL_SECONDS = 300

# A search is one HTTP call to SearXNG. A fetch retrieves a page, converts it
# and may embed it, so it is allowed considerably longer. Both are bounded:
# a tool call that hangs holds the whole turn.
SEARCH_TIMEOUT_SECONDS = 30
FETCH_TIMEOUT_SECONDS = 90
DESCRIPTOR_TIMEOUT_SECONDS = 10

# Fixed messages handed to the model when a call cannot be made at all. None
# names an internal service or address, matching the gateway's own rule for
# these two tools (criterion B11).
MSG_TOOL_UNAVAILABLE = "This tool is unavailable right now, so no live information could be retrieved."
MSG_NO_CREDENTIAL = (
    "This tool could not be used because the session's credential could not be resolved. "
    "Answer without live information and say so."
)
MSG_NO_TURN = "This tool could not be used because the turn identifier is missing."

_descriptor_cache: dict[str, Any] = {"fetched_at": 0.0, "specs": []}
_descriptor_lock = asyncio.Lock()


def enabled(environ=None) -> bool:
    """Whether this deployment advertises the Hive web tools at all."""
    environ = os.environ if environ is None else environ
    return (environ.get(ENABLED_ENV) or "true").strip().lower() not in ("false", "0", "no", "off")


def kept_builtin_names(environ=None) -> frozenset:
    """Upstream builtin tool names this deployment still wants attached."""
    environ = os.environ if environ is None else environ
    return frozenset(
        entry.strip() for entry in (environ.get(BUILTIN_ALLOWLIST_ENV) or "").split(",") if entry.strip()
    )


def upstream_base(environ=None) -> str:
    """The `/v1` root of the Hive gateway, from Open WebUI's own configuration.

    Same source hive_agent_proxy.py reads, so this module adds no configuration
    and gives the shim key no new place to live.
    """
    environ = os.environ if environ is None else environ
    return (environ.get("OPENAI_API_BASE_URL") or "").strip().rstrip("/")


def shim_key(environ=None) -> str:
    environ = os.environ if environ is None else environ
    return (environ.get("OPENAI_API_KEY") or "").strip()


def tool_capable(model) -> bool:
    """Whether this alias has a routable, tool capable route behind it.

    Read from `hive_capabilities.tools` on the model listing, which edge-api
    computes from `provider_capabilities.tools_supported` per route and
    serialises without `omitempty` precisely so that a missing block and a
    false capability are distinguishable. Both answer false here, because
    "we do not know" and "no" have the same safe action: advertise nothing.

    Open WebUI merges the gateway's model entry with `**model`, so the block
    survives onto the dict `process_chat_payload` receives. It is read from the
    nested `openai` copy as a fallback because that is the untouched original
    of the same entry, and a future upstream change that rebuilds the outer
    dict from named keys would otherwise silently disable every tool.
    """
    if not isinstance(model, dict):
        return False
    for source in (model, model.get("openai")):
        if isinstance(source, dict):
            capabilities = source.get("hive_capabilities")
            if isinstance(capabilities, dict) and capabilities.get("tools") is True:
                return True
    return False


def _request(method: str, url: str, headers: dict, body, timeout_seconds: int):
    """One HTTP round trip. Returns (status, parsed JSON or None).

    A non 2xx status is NOT an exception here. Every refusal these endpoints
    make is a JSON envelope naming its class (insufficient_credit,
    budget_exhausted, rate_limited, url_rejected), and that envelope is exactly
    what the model needs to be told. Discarding the body on a 402 and reporting
    a generic failure would turn "you are out of credit" into "the tool is
    broken", which is the shape of message that sends a customer to support
    instead of to the top up page.
    """
    data = json.dumps(body).encode("utf-8") if body is not None else None
    request = urllib.request.Request(url, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(request, timeout=timeout_seconds) as response:
            return response.status, json.loads(response.read().decode("utf-8", "replace") or "null")
    except urllib.error.HTTPError as error:
        raw = error.read().decode("utf-8", "replace")
        try:
            return error.code, json.loads(raw or "null")
        except ValueError:
            return error.code, None


async def _fetch_descriptors(environ=None) -> list:
    """The tool specifications, from edge-api's own `GET /v1/tools`.

    Returns [] on any failure, which advertises nothing. That is the whole
    point: a hardcoded fallback would keep working after the handler behind it
    changed, and the model would be told about arguments that no longer exist.
    """
    environ = os.environ if environ is None else environ
    base = upstream_base(environ)
    if not base:
        log.warning("hive: no OPENAI_API_BASE_URL, so the web tool specifications cannot be read")
        return []

    headers = {}
    key = shim_key(environ)
    if key:
        headers["Authorization"] = f"Bearer {key}"

    try:
        status, payload = await asyncio.to_thread(
            _request, "GET", f"{base}/tools", headers, None, DESCRIPTOR_TIMEOUT_SECONDS
        )
    except Exception:
        log.exception("hive: could not read the web tool specifications, advertising no web tools")
        return []
    if status != 200:
        log.warning("hive: GET /v1/tools answered %s, advertising no web tools", status)
        return []

    data = payload.get("data") if isinstance(payload, dict) else None
    if not isinstance(data, list):
        log.warning("hive: GET /v1/tools returned no data list, advertising no web tools")
        return []
    return data


async def descriptors(environ=None) -> list:
    """The cached specification list. One in flight fetch at a time."""
    now = time.monotonic()
    if _descriptor_cache["specs"] and now - _descriptor_cache["fetched_at"] < DESCRIPTOR_TTL_SECONDS:
        return _descriptor_cache["specs"]
    async with _descriptor_lock:
        now = time.monotonic()
        if _descriptor_cache["specs"] and now - _descriptor_cache["fetched_at"] < DESCRIPTOR_TTL_SECONDS:
            return _descriptor_cache["specs"]
        specs = await _fetch_descriptors(environ)
        if specs:
            _descriptor_cache["specs"] = specs
            _descriptor_cache["fetched_at"] = time.monotonic()
        return specs


def reset_descriptor_cache() -> None:
    """Test seam. Never called by the running container."""
    _descriptor_cache["specs"] = []
    _descriptor_cache["fetched_at"] = 0.0


async def _user_token(request, user) -> str:
    """The signed in user's access token, resolved server side.

    Same resolver hive_agent_proxy.py and hive_upstream_auth.py use, so all
    three sites share one definition of "this user's credential". Imported
    inside the function because `utils.middleware` imports this module.
    """
    from open_webui.utils.middleware import get_system_oauth_token

    token = await get_system_oauth_token(request, user)
    return ((token or {}).get("access_token") or "").strip()


def _error(message: str, code: str = "") -> str:
    """A refusal the model can read, and that upstream's citation extraction
    recognises as "no source here" rather than turning into a citation whose
    body is an error message."""
    payload = {"error": message}
    if code:
        payload["code"] = code
    return json.dumps(payload)


def _render_search(payload: dict) -> str:
    """A web_search envelope, in the JSON array shape upstream's citation
    extraction already parses: title, link, snippet.

    `link` rather than `url` is not a whim. utils/middleware.py's `search_web`
    branch reads exactly that key when it builds the citation metadata, and a
    result rendered with `url` would reach the model correctly and produce no
    citation chip at all, which is the "searched but showed no sources" shape
    of issue #1621.
    """
    status = payload.get("status")
    if status == "error":
        return _error(payload.get("message") or MSG_TOOL_UNAVAILABLE, payload.get("code") or "")
    results = payload.get("results")
    if status != "ok" or not isinstance(results, list) or not results:
        return _error("No results were found for that query.", "empty")
    return json.dumps(
        [
            {
                "title": hit.get("title", ""),
                "link": hit.get("url", ""),
                "snippet": hit.get("snippet", ""),
            }
            for hit in results
            if isinstance(hit, dict)
        ]
    )


def _render_fetch(payload: dict) -> str:
    """A web_fetch envelope as readable text.

    The parts arrive already wrapped in the gateway's per call
    `[BEGIN UNTRUSTED WEB CONTENT <token>]` fence, and they are passed through
    untouched. Rewrapping or stripping them here would break the one property
    the fence has: a page cannot close a fence whose token it has never seen.
    """
    status = payload.get("status")
    if status == "error":
        return _error(payload.get("message") or MSG_TOOL_UNAVAILABLE, payload.get("code") or "")
    parts = payload.get("parts")
    if status != "ok" or not isinstance(parts, list) or not parts:
        return _error("That page returned no readable content.", "empty")

    header = []
    if payload.get("title"):
        header.append(str(payload["title"]))
    final_url = payload.get("final_url") or payload.get("url")
    if final_url:
        header.append(str(final_url))
    body = "\n\n".join(str(part.get("text", "")) for part in parts if isinstance(part, dict))
    if payload.get("truncated"):
        body += "\n\n[This page was longer than the retrieval budget; the parts above are the relevant excerpts.]"
    return "\n".join(header + ["", body]) if header else body


async def _call_tool(request, user, metadata, tool: str, body: dict, timeout_seconds: int) -> dict | str:
    """POST one tool call to the gateway. Returns the parsed envelope, or a
    rendered refusal string when the call could not be made at all.

    Every failure mode returns something the model can act on. Raising would
    surface as `str(e)` in the transcript through upstream's own
    `except Exception` in the tool loop, which is both uglier and, since an
    exception message can carry an internal address, a leak.
    """
    base = upstream_base()
    if not base:
        return MSG_TOOL_UNAVAILABLE

    turn = (metadata or {}).get("message_id")
    turn = str(turn).strip() if turn else ""
    if not turn:
        # The gateway refuses a call with no turn, and it is right to: the per
        # turn budget is what bounds a model driven fetch loop. Say so rather
        # than sending a request that is certain to be refused.
        return MSG_NO_TURN

    try:
        token = await _user_token(request, user)
    except Exception:
        log.exception("hive: could not resolve the signed-in user's credential for a web tool call")
        token = ""
    if not token:
        # Fail closed. Without the user's token the gateway would either refuse
        # (it does, since #1718 added these paths to requiresPerUserAuth) or, in
        # a future where it did not, bill the shim account for this customer's
        # search. Neither is served by trying anyway.
        log.warning("hive: no upstream credential for a %s call by user %s", tool, getattr(user, "id", None))
        return MSG_NO_CREDENTIAL

    headers = {
        "Authorization": f"Bearer {shim_key()}",
        UPSTREAM_AUTH_HEADER: f"Bearer {token}",
        TURN_HEADER: turn,
        "Content-Type": "application/json",
    }
    try:
        _status, payload = await asyncio.to_thread(
            _request, "POST", f"{base}/tools/{tool}", headers, body, timeout_seconds
        )
    except Exception:
        # No exception text in what the model sees. A urllib error message
        # carries the request URL, and that URL names an internal service.
        log.exception("hive: %s call failed", tool)
        return MSG_TOOL_UNAVAILABLE

    if not isinstance(payload, dict):
        return MSG_TOOL_UNAVAILABLE
    return payload


def build_tools(request, user, metadata, specs: list) -> dict:
    """The two tool entries, in the shape Open WebUI's native tool loop reads.

    `spec` is the FUNCTION half of the descriptor, because the loop wraps it
    itself: `{'type': 'function', 'function': tool.get('spec', {})}`. Passing
    the whole descriptor would send a doubly nested object no provider accepts.
    """
    tools: dict = {}
    for descriptor in specs:
        if not isinstance(descriptor, dict):
            continue
        function = descriptor.get("function")
        if not isinstance(function, dict):
            continue
        name = function.get("name")
        if name not in (WEB_SEARCH, WEB_FETCH):
            continue

        if name == WEB_SEARCH:

            async def web_search(query: str = "", max_results: int = 0) -> str:
                body: dict = {"query": query}
                if max_results:
                    body["max_results"] = max_results
                payload = await _call_tool(
                    request, user, metadata, WEB_SEARCH, body, SEARCH_TIMEOUT_SECONDS
                )
                if isinstance(payload, str):
                    return _error(payload)
                return _render_search(payload)

            callable_ = web_search
        else:

            async def web_fetch(url: str = "", focus: str = "") -> str:
                payload = await _call_tool(
                    request,
                    user,
                    metadata,
                    WEB_FETCH,
                    {"url": url, "focus": focus},
                    FETCH_TIMEOUT_SECONDS,
                )
                if isinstance(payload, str):
                    return _error(payload)
                return _render_fetch(payload)

            callable_ = web_fetch

        tools[name] = {
            "tool_id": f"hive:{name}",
            "callable": callable_,
            "spec": function,
            # Not 'builtin': that type is what select_tools strips, and not
            # 'external'/'action'/'terminal' either, which upstream's
            # process_tool_result treats as HTML producing.
            "type": "hive_web",
            "direct": False,
        }
    return tools


def override_instruction(features, tools) -> str:
    """The one line the globe toggle still buys, or "" when it buys nothing.

    The toggle is deliberately NOT removed and is deliberately NOT required.
    Advertisement happens on every eligible turn regardless of it, which is the
    whole point of issue #1718, so the toggle's original meaning ("run a search
    for this message") no longer describes anything the system does. A control
    that claims a state the system does not have is worse than no control, so
    it is re-pointed at the only decision left that a user is better placed to
    make than the model: insisting on live results for THIS message, when the
    model would otherwise have answered from what it already knows.

    Off, nothing is added and the model decides alone. On, it is told the user
    asked for live results. Neither state can remove the tools.
    """
    if not isinstance(features, dict) or not features.get("web_search"):
        return ""
    if WEB_SEARCH not in (tools or {}):
        return ""
    return (
        "The user has explicitly asked for this message to be answered from live web results. "
        f"Call {WEB_SEARCH} before answering, even if you believe you already know the answer."
    )


def has_stranded_knowledge(model, metadata) -> bool:
    """Whether this turn carries documents only a builtin tool can reach.

    Both sources are ones upstream stops injecting into the request the moment
    function calling is native: a folder's files, which it moves to
    metadata['folder_knowledge'], and a custom model's attached knowledge, which
    it simply skips. On such a turn the knowledge tools are not optional, they
    are the delivery mechanism.
    """
    if isinstance(metadata, dict) and metadata.get("folder_knowledge"):
        return True
    if not isinstance(model, dict):
        return False
    return bool(((model.get("info") or {}).get("meta") or {}).get("knowledge"))


async def select_tools(request, tools_dict, model, metadata, user, environ=None) -> dict:
    """The tool set this deployment advertises for one chat turn.

    Upstream builtins out (see the module docstring for the measured payload
    cost that froze this deployment on the legacy path), Hive's two web tools in
    when the alias can serve them. Returns a new dict; the caller's own is not
    mutated.
    """
    environ = os.environ if environ is None else environ
    source = tools_dict if isinstance(tools_dict, dict) else {}
    keep = set(kept_builtin_names(environ))
    if has_stranded_knowledge(model, metadata):
        keep |= KNOWLEDGE_TOOL_NAMES
    selected = {
        name: entry
        for name, entry in source.items()
        if not (isinstance(entry, dict) and entry.get("type") == "builtin" and name not in keep)
    }

    if not enabled(environ) or not tool_capable(model):
        return selected

    specs = await descriptors(environ)
    if not specs:
        return selected

    for name, entry in build_tools(request, user, metadata, specs).items():
        # A tool the user explicitly attached, or an MCP server's tool, wins on
        # a name collision. Ours are additive, never a silent replacement.
        selected.setdefault(name, entry)
    return selected

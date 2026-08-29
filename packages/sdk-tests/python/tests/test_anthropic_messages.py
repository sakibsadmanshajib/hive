"""Anthropic Messages API conformance, exercised with the real `anthropic` SDK.

Every test in this file drives the genuine, PyPI-published `anthropic` Python
client (never a hand-rolled HTTP request) against Hive's `/v1/messages`
surface. The point is that the SDK's own request serialization, streaming
event parser and typed exception classes accept what Hive sends -- a curl
that succeeds where the SDK fails is exactly the class of bug this file
exists to catch.

Base URL convention deliberately differs from every other file in this
package: the Anthropic SDK appends `/v1/messages` itself, so its `base_url`
must NOT include the `/v1` suffix the OpenAI-compatible suites use (see
docs/anthropic-sdk-integration.md). `_anthropic_base_url()` derives it from
the same `HIVE_BASE_URL` the rest of this package already reads, so no new
environment knob is needed and tools/lint-sdk-test-env-propagation.mjs has
nothing new to track for this pair.

No test here is skipped for missing credentials. `HIVE_API_KEY` has an
in-code fallback ("test-key", matching every other file in this package) so
collection never aborts, but a missing/invalid key makes every call fail
loudly with a real `AuthenticationError` rather than silently skipping --
this repo has previously shipped 62 silent-skip sites that hid real gaps.

Model split, matching the convention `chat-completions.test.ts` and
`docker-compose.yml` already established (issue #1088, PR fix/tools-model-
cheap-default): `MODEL` (default `hive-free`, the free-pool alias D-047
mandates for automated consumption) for anything that only needs a plain
chat completion, `TOOLS_MODEL` (default `deepseek-v4-flash`, the cheap
tools-capable alias) for anything that needs `tools_supported=true`.
`hive-free` and `hive-default` are not declared tools-capable in the
catalog (`GET /catalog/models`), so tool tests never use `MODEL`.

Two findings below are asserted as `xfail`, not skipped and not silently
loosened: `count_tokens` under API-key auth (issue #1261) and
`models.list()` under the SDK's default `x-api-key` auth (issue #1259).
`xfail` still runs the real call every time, still fails the run loudly if
the bug is ever fixed without updating this file (`strict=True`), and keeps
the fact that these are currently broken visible in every CI report instead
of hidden behind a passing assertion that quietly accepted the bug as
correct behavior.
"""

import os

import httpx
import pytest
from anthropic import Anthropic, APIStatusError, AuthenticationError, BadRequestError, NotFoundError

BASE_URL = os.getenv("HIVE_BASE_URL", "http://localhost:8080/v1")
API_KEY = os.getenv("HIVE_API_KEY", "test-key")
MODEL = os.getenv("HIVE_TEST_MODEL", "hive-free")
TOOLS_MODEL = os.getenv("HIVE_TOOLS_MODEL", "deepseek-v4-flash")

# A gateway-side 4 MiB cap on the raw request body (apps/edge-api/internal/
# anthropic/handler.go maxBodyBytes) truncates anything larger mid-stream
# before json.Unmarshal ever sees a complete document, so the resulting
# "invalid JSON body" message is accurate for what actually reached the
# parser, not misleading -- see test_oversized_body_* below.
OVERSIZED_BYTES = 5 * 1024 * 1024

TINY_PNG_BASE64 = (
    "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk"
    "+A8AAQUBAScY42YAAAAASUVORK5CYII="
)


def _anthropic_base_url() -> str:
    # The OpenAI-compatible suites in this package point HIVE_BASE_URL at a
    # `/v1`-suffixed URL. The Anthropic SDK appends `/v1/messages` itself, so
    # the same env var must be de-suffixed for this client (see module
    # docstring and docs/anthropic-sdk-integration.md "Base URL").
    return BASE_URL[: -len("/v1")] if BASE_URL.endswith("/v1") else BASE_URL


@pytest.fixture
def client() -> Anthropic:
    return Anthropic(base_url=_anthropic_base_url(), api_key=API_KEY)


WEATHER_TOOL = {
    "name": "get_weather",
    "description": "Get the current weather for a city.",
    "input_schema": {
        "type": "object",
        "properties": {"city": {"type": "string"}},
        "required": ["city"],
    },
}


# --- messages.create: non-streaming and streaming ---------------------------


def test_messages_create_basic_non_streaming(client: Anthropic) -> None:
    """The real SDK can call messages.create and receive a well-formed reply."""
    msg = client.messages.create(
        model=MODEL, max_tokens=64, messages=[{"role": "user", "content": "Say hello in one short sentence."}]
    )
    assert msg.type == "message"
    assert msg.role == "assistant"
    assert msg.id.startswith("msg_")
    assert msg.model == MODEL
    assert msg.stop_reason in {"end_turn", "max_tokens", "stop_sequence", "tool_use"}
    assert isinstance(msg.usage.input_tokens, int) and msg.usage.input_tokens >= 0
    assert isinstance(msg.usage.output_tokens, int) and msg.usage.output_tokens >= 0


@pytest.mark.xfail(
    reason="issue #1274: content_block_start omits an empty text field "
    "(json:\"text,omitempty\"), so the SDK's own stream accumulator crashes "
    "with TypeError on the first content_block_delta of any text response",
    strict=True,
)
def test_streaming_event_sequence_integrity(client: Anthropic) -> None:
    """The SDK's strict streaming parser accepts Hive's Anthropic event sequence.

    Anthropic's documented order is message_start, then zero or more
    content_block_start/delta*/stop groups, then a single message_delta,
    then message_stop. The SDK raises on a malformed or out-of-order event,
    so simply not raising while iterating is itself a real assertion.
    """
    event_types: list[str] = []
    with client.messages.stream(
        model=MODEL, max_tokens=64, messages=[{"role": "user", "content": "Count from one to five."}]
    ) as stream:
        for event in stream:
            event_types.append(event.type)
        final = stream.get_final_message()

    assert event_types[0] == "message_start"
    assert event_types[-2:] == ["message_delta", "message_stop"]
    assert event_types.count("message_delta") == 1, "message_delta must be cumulative, emitted exactly once"
    assert final.id.startswith("msg_")
    assert final.usage.output_tokens is not None and final.usage.output_tokens >= 0


# --- system: plain string vs typed content-block array -----------------------


def test_system_as_plain_string(client: Anthropic) -> None:
    msg = client.messages.create(
        model=MODEL, max_tokens=32, system="Reply in exactly one word.", messages=[{"role": "user", "content": "hi"}]
    )
    assert msg.stop_reason in {"end_turn", "max_tokens"}


def test_system_as_typed_content_block_array_with_cache_control(client: Anthropic) -> None:
    """A typed system content-block array with a cache_control breakpoint
    must survive translation (PR #1152 fixed two sites that flattened typed
    content to plain strings, silently destroying client-side caching)."""
    msg = client.messages.create(
        model=MODEL,
        max_tokens=32,
        system=[
            {
                "type": "text",
                "text": "You are a terse assistant. " * 40,
                "cache_control": {"type": "ephemeral"},
            }
        ],
        messages=[{"role": "user", "content": "hi"}],
    )
    assert msg.stop_reason in {"end_turn", "max_tokens"}
    # Anthropic-exclusive shape: these fields exist and are never negative,
    # whether or not the upstream provider actually cached anything.
    assert msg.usage.cache_creation_input_tokens is None or msg.usage.cache_creation_input_tokens >= 0
    assert msg.usage.cache_read_input_tokens is None or msg.usage.cache_read_input_tokens >= 0


def test_multi_block_user_content_array(client: Anthropic) -> None:
    """A multi-block content array is accepted end to end by the real SDK."""
    msg = client.messages.create(
        model=MODEL,
        max_tokens=32,
        messages=[
            {
                "role": "user",
                "content": [
                    {"type": "text", "text": "Part one: remember the number 7."},
                    {"type": "text", "text": "Part two: reply with just that number."},
                ],
            }
        ],
    )
    assert msg.stop_reason in {"end_turn", "max_tokens"}


# --- tools: auto/any/tool/none, round trip, disable_parallel_tool_use --------


def test_tool_choice_auto_produces_tool_use_and_round_trips(client: Anthropic) -> None:
    first = client.messages.create(
        model=TOOLS_MODEL,
        max_tokens=200,
        tools=[WEATHER_TOOL],
        tool_choice={"type": "auto"},
        messages=[{"role": "user", "content": "What is the weather in Dhaka? You must use the get_weather tool."}],
    )
    assert first.stop_reason == "tool_use"
    tool_use = next((b for b in first.content if b.type == "tool_use"), None)
    assert tool_use is not None, "model did not call the tool with tool_choice=auto"
    assert tool_use.name == "get_weather"
    assert isinstance(tool_use.input, dict)

    second = client.messages.create(
        model=TOOLS_MODEL,
        max_tokens=100,
        tools=[WEATHER_TOOL],
        messages=[
            {"role": "user", "content": "What is the weather in Dhaka? You must use the get_weather tool."},
            {"role": "assistant", "content": first.content},
            {
                "role": "user",
                "content": [
                    {"type": "tool_result", "tool_use_id": tool_use.id, "content": "72F and sunny"}
                ],
            },
        ],
    )
    assert second.stop_reason in {"end_turn", "max_tokens"}
    assert any(b.type == "text" and b.text for b in second.content)


def test_tool_choice_any_forces_a_tool_call(client: Anthropic) -> None:
    msg = client.messages.create(
        model=TOOLS_MODEL,
        max_tokens=100,
        tools=[WEATHER_TOOL],
        tool_choice={"type": "any"},
        messages=[{"role": "user", "content": "What is the weather in Dhaka?"}],
    )
    assert any(b.type == "tool_use" for b in msg.content)


def test_tool_choice_named_tool_forces_that_tool(client: Anthropic) -> None:
    msg = client.messages.create(
        model=TOOLS_MODEL,
        max_tokens=100,
        tools=[WEATHER_TOOL],
        tool_choice={"type": "tool", "name": "get_weather"},
        messages=[{"role": "user", "content": "What is the weather in Dhaka?"}],
    )
    tool_use = next((b for b in msg.content if b.type == "tool_use"), None)
    assert tool_use is not None
    assert tool_use.name == "get_weather"


@pytest.mark.xfail(
    reason="issue #1260: empty completions serialize content as JSON null, not [], "
    "breaking any client (including this SDK) that assumes content is iterable",
    strict=True,
)
def test_tool_choice_none_forbids_tool_use(client: Anthropic) -> None:
    msg = client.messages.create(
        model=TOOLS_MODEL,
        max_tokens=100,
        tools=[WEATHER_TOOL],
        tool_choice={"type": "none"},
        messages=[{"role": "user", "content": "What is the weather in Dhaka?"}],
    )
    assert msg.content is not None
    assert not any(b.type == "tool_use" for b in msg.content)


def test_disable_parallel_tool_use(client: Anthropic) -> None:
    msg = client.messages.create(
        model=TOOLS_MODEL,
        max_tokens=200,
        tools=[WEATHER_TOOL],
        tool_choice={"type": "auto", "disable_parallel_tool_use": True},
        messages=[
            {
                "role": "user",
                "content": "What is the weather in Dhaka and in Sylhet? Use the tool once per city.",
            }
        ],
    )
    tool_calls = [b for b in msg.content if b.type == "tool_use"]
    assert len(tool_calls) <= 1, "disable_parallel_tool_use must forbid more than one simultaneous tool call"


# --- sampling params, stop_sequences, metadata.user_id -----------------------


def test_max_tokens_stop_sequences_and_metadata_user_id(client: Anthropic) -> None:
    msg = client.messages.create(
        model=MODEL,
        max_tokens=32,
        stop_sequences=["7777"],
        metadata={"user_id": "sdk-conformance-test"},
        messages=[{"role": "user", "content": "Count up from one, one number per line."}],
    )
    assert msg.stop_reason in {"end_turn", "max_tokens", "stop_sequence"}


def test_temperature_top_p_top_k_via_extra_body(client: Anthropic) -> None:
    """temperature/top_p/top_k have no typed keyword on the current, real
    `anthropic` SDK's messages.create() (verified live against SDK 1.2.0: the
    call raises TypeError for an unexpected keyword). They can still reach
    Hive via the SDK's documented extra_body escape hatch, which is what any
    caller building on an unmodified SDK would have to do too."""
    msg = client.messages.create(
        model=MODEL,
        max_tokens=32,
        messages=[{"role": "user", "content": "Say hello."}],
        extra_body={"temperature": 0.5, "top_p": 0.9, "top_k": 40},
    )
    assert msg.stop_reason in {"end_turn", "max_tokens"}


def test_thinking_param_is_silently_ignored(client: Anthropic) -> None:
    """Neither configured provider supports Anthropic-native extended
    thinking (docs/anthropic-sdk-integration.md, Known limitations). The
    request must still be accepted, not rejected, matching that documented
    behavior."""
    msg = client.messages.create(
        model=MODEL,
        max_tokens=32,
        thinking={"type": "enabled", "budget_tokens": 16},
        messages=[{"role": "user", "content": "2+2?"}],
    )
    assert msg.stop_reason in {"end_turn", "max_tokens"}


# --- vision -------------------------------------------------------------


def test_vision_image_block_is_translated_and_dispatched(client: Anthropic) -> None:
    """An image content block must be accepted and translated (never
    rejected by Hive's own request validation); whether the resolved route
    actually serves vision is a provider-capability question, not this
    surface's. Either a real completion or a well-typed upstream error is
    acceptable; a local 400 from Hive's own parsing is not."""
    try:
        msg = client.messages.create(
            model=MODEL,
            max_tokens=32,
            messages=[
                {
                    "role": "user",
                    "content": [
                        {
                            "type": "image",
                            "source": {"type": "base64", "media_type": "image/png", "data": TINY_PNG_BASE64},
                        },
                        {"type": "text", "text": "One word: what color is this image?"},
                    ],
                }
            ],
        )
        assert msg.stop_reason in {"end_turn", "max_tokens"}
    except APIStatusError as e:
        # A provider-side refusal (e.g. the resolved route has no vision
        # endpoint) is acceptable; it proves the image block reached
        # dispatch rather than being rejected by local validation.
        assert e.status_code >= 400


# --- count_tokens and models.list --------------------------------------


@pytest.mark.xfail(
    reason="issue #1261: count_tokens requires a session principal and 401s "
    "every valid API-key caller, unlike every other endpoint on this surface",
    strict=True,
)
def test_count_tokens(client: Anthropic) -> None:
    result = client.messages.count_tokens(model=MODEL, messages=[{"role": "user", "content": "hello world"}])
    assert result.input_tokens > 0


@pytest.mark.xfail(
    reason="issue #1259: GET /v1/models does not accept the SDK's default "
    "x-api-key header and answers with the OpenAI error envelope instead of "
    "the Anthropic one",
    strict=True,
)
def test_models_list(client: Anthropic) -> None:
    page = client.models.list()
    ids = [m.id for m in page.data]
    assert MODEL in ids or TOOLS_MODEL in ids


# --- error surfaces: typed exceptions, not generic ones ----------------


def test_invalid_model_is_not_found_not_a_defect(client: Anthropic) -> None:
    """No Claude model exists in Hive's catalog and none will be added
    (owner decision 2026-08-26, DeepSeek and Groq only). A claude-* model
    name answering 404 is expected, correct behavior, not a gap."""
    with pytest.raises(NotFoundError) as exc_info:
        client.messages.create(
            model="claude-3-5-sonnet-20241022", max_tokens=16, messages=[{"role": "user", "content": "hi"}]
        )
    assert exc_info.value.status_code == 404
    assert exc_info.value.body["type"] == "error"
    assert exc_info.value.body["error"]["type"] == "not_found_error"


def test_empty_messages_is_bad_request(client: Anthropic) -> None:
    with pytest.raises(BadRequestError) as exc_info:
        client.messages.create(model=MODEL, max_tokens=16, messages=[])
    assert exc_info.value.status_code == 400


def test_invalid_api_key_is_authentication_error() -> None:
    bad_client = Anthropic(base_url=_anthropic_base_url(), api_key="hk_conformance_test_bogus_key")
    with pytest.raises(AuthenticationError) as exc_info:
        bad_client.messages.create(model=MODEL, max_tokens=16, messages=[{"role": "user", "content": "hi"}])
    assert exc_info.value.status_code == 401
    assert exc_info.value.body["type"] == "error"
    assert exc_info.value.body["error"]["type"] == "authentication_error"


def test_oversized_body_is_bad_request_not_a_hang_or_500() -> None:
    """A body over the gateway's 4 MiB cap (apps/edge-api/internal/anthropic/
    handler.go maxBodyBytes) is truncated by io.LimitReader before
    json.Unmarshal runs, so it genuinely is invalid JSON once truncated --
    "invalid JSON body" is an accurate description of what was parsed, not a
    misleading one for a well-formed request that merely arrived too large.
    A real, plausible agent payload (a long conversation, a large system
    prompt, many tool definitions) can realistically cross this ceiling; this
    test only asserts the SDK gets a typed 400 rather than a hang, a raw
    connection reset, or a 500."""
    client_ = Anthropic(base_url=_anthropic_base_url(), api_key=API_KEY, max_retries=0)
    with pytest.raises(BadRequestError) as exc_info:
        client_.messages.create(
            model=MODEL,
            max_tokens=16,
            system="x" * OVERSIZED_BYTES,
            messages=[{"role": "user", "content": "hi"}],
        )
    assert exc_info.value.status_code == 400


# --- provider-identity leak guard ---------------------------------------


def test_no_provider_identity_leaks_into_response(client: Anthropic) -> None:
    """Provider names, upstream ids and system_fingerprint must never reach
    a caller (PR #1222). Checked on the raw response body, not just the
    typed fields, so a leak in an untyped/extra field would still be caught."""
    msg = client.messages.create(model=MODEL, max_tokens=16, messages=[{"role": "user", "content": "hi"}])
    raw = msg.model_dump_json().lower()
    for leak_term in ("openrouter", "groq", "route-", "gen-", "system_fingerprint"):
        assert leak_term not in raw, f"provider-identity leak: {leak_term!r} found in response body"
    assert msg.id.startswith("msg_")
    assert msg.model == MODEL


def test_no_provider_identity_leaks_in_error_body() -> None:
    """The same guard on an error path: an upstream refusal must reach the
    client through the sanitizer, never with a raw provider exception class
    or route id attached."""
    raw = httpx.post(
        _anthropic_base_url() + "/v1/messages",
        headers={"x-api-key": API_KEY, "content-type": "application/json"},
        json={"model": "definitely-not-a-real-alias", "max_tokens": 16, "messages": [{"role": "user", "content": "hi"}]},
        timeout=15,
    )
    body_lower = raw.text.lower()
    for leak_term in ("openrouter", "groq", "route-"):
        assert leak_term not in body_lower, f"provider-identity leak in error body: {leak_term!r}"

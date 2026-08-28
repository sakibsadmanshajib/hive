#!/usr/bin/env python3
"""Self-check for verify-rag-roundtrip.py's check_stream_frames, the pure
predicate stream_leak_check applies against a live /v1/rag/chat streaming
response. No framework, no network: exercises the pure function directly
against synthetic SSE frame payloads, same style as
scripts/test_seed_demo_owner.py. Run: python3 scripts/test_verify_rag_roundtrip.py
"""
import importlib.util
import json
from pathlib import Path

spec = importlib.util.spec_from_file_location(
    "verify_rag_roundtrip", Path(__file__).parent / "verify-rag-roundtrip.py"
)
verify_rag_roundtrip = importlib.util.module_from_spec(spec)
spec.loader.exec_module(verify_rag_roundtrip)

check_stream_frames = verify_rag_roundtrip.check_stream_frames


def frame(**kwargs) -> str:
    return json.dumps(kwargs)


def main() -> None:
    alias = "hive-fast"

    # Clean stream: one content chunk, one finish_reason chunk, one
    # usage-only terminal frame, all sharing the same gateway-minted id.
    clean = [
        frame(id="ragchat-abc", model=alias,
              choices=[{"index": 0, "delta": {"content": "hi"}, "finish_reason": None}]),
        frame(id="ragchat-abc", model=alias,
              choices=[{"index": 0, "delta": {"content": " there"}, "finish_reason": "stop"}]),
        frame(id="ragchat-abc", model=alias, choices=[], usage={"total_tokens": 15}),
    ]
    assert check_stream_frames(clean, alias) == [], check_stream_frames(clean, alias)

    # Post-finish leak: a spurious chunk after finish_reason, no usage --
    # exactly the DeepSeek-family-via-OpenRouter shape PR #1222 found on the
    # inference path and this fix closes on the RAG path.
    post_finish_leak = [
        frame(id="ragchat-abc", model=alias,
              choices=[{"index": 0, "delta": {"content": "hi"}, "finish_reason": "stop"}]),
        frame(id="ragchat-abc", model=alias,
              choices=[{"index": 0, "delta": {"role": "assistant", "content": ""}, "finish_reason": None}]),
    ]
    violations = check_stream_frames(post_finish_leak, alias)
    assert any("post-finish" in v for v in violations), violations

    # Usage-only terminal frame after finish_reason is the ONE legitimate
    # exception and must never be flagged.
    usage_only_after_finish = [
        frame(id="ragchat-abc", model=alias,
              choices=[{"index": 0, "delta": {"content": "hi"}, "finish_reason": "stop"}]),
        frame(id="ragchat-abc", model=alias, choices=[], usage={"total_tokens": 5}),
    ]
    assert check_stream_frames(usage_only_after_finish, alias) == [], \
        check_stream_frames(usage_only_after_finish, alias)

    # Empty-but-present usage after finish_reason. The server's rule is
    # `chunk.Usage != nil` (inference.ShouldSuppressPostFinishChunk), so it
    # forwards this frame; a truthiness test on the client would call the same
    # forwarded frame a leak. This asserts the two rules agree.
    empty_usage_after_finish = [
        frame(id="ragchat-abc", model=alias,
              choices=[{"index": 0, "delta": {"content": "hi"}, "finish_reason": "stop"}]),
        frame(id="ragchat-abc", model=alias, choices=[], usage={}),
    ]
    assert check_stream_frames(empty_usage_after_finish, alias) == [], \
        check_stream_frames(empty_usage_after_finish, alias)

    # Raw upstream id (OpenRouter gen-* shape) never minted by the gateway.
    raw_id_leak = [frame(id="gen-1787734315-abc", model=alias, choices=[])]
    violations = check_stream_frames(raw_id_leak, alias)
    assert any("not gateway-minted" in v or "gen-" in v for v in violations), violations

    # system_fingerprint key surviving onto the wire, even with a clean id.
    fingerprint_leak = '{"id":"ragchat-abc","model":"hive-fast","system_fingerprint":"fp_44709d6fcb","choices":[]}'
    violations = check_stream_frames([fingerprint_leak], alias)
    assert any("system_fingerprint" in v for v in violations), violations

    # Provider name string anywhere on the wire.
    provider_name_leak = '{"id":"ragchat-abc","model":"hive-fast","choices":[],"note":"routed via groq"}'
    violations = check_stream_frames([provider_name_leak], alias)
    assert any("groq" in v for v in violations), violations

    # Two different ids on one stream: client-visible id must be stable.
    unstable_id = [
        frame(id="ragchat-abc", model=alias, choices=[]),
        frame(id="ragchat-def", model=alias, choices=[]),
    ]
    violations = check_stream_frames(unstable_id, alias)
    assert any("multiple distinct ids" in v for v in violations), violations

    # Upstream route name surviving instead of the requested alias.
    model_leak = [frame(id="ragchat-abc", model="route-groq-fast", choices=[])]
    violations = check_stream_frames(model_leak, alias)
    assert any("differs from the requested alias" in v for v in violations), violations

    # Unparseable frame is reported, not silently ignored.
    violations = check_stream_frames(["not json"], alias)
    assert any("not valid JSON" in v for v in violations), violations

    print("ok: verify-rag-roundtrip.py check_stream_frames leak/post-finish assertions")


if __name__ == "__main__":
    main()

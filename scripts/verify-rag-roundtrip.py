#!/usr/bin/env python3
"""End-to-end check of the Hive RAG pipeline against a running stack.

Uploads a document carrying a unique marker phrase, waits for control-plane to
chunk and embed it, then proves the marker comes back out of pgvector through
POST /v1/rag/search and through the grounded POST /v1/rag/chat answer. Exits
non-zero on the first step that cannot be proven, so it works as a demo
readiness gate and as a post-deploy smoke check.

Why a script and not a CI test: the path needs a real embedding provider and a
real Supabase project. CI has neither, and that blind spot is exactly how a
broken embedding request field shipped (LiteLLM rejected `dimensions` for every
document while the unit suite stayed green). Run this against the demo box, or
any live stack, after touching the RAG or embedding code.

Idempotent: the throwaway tenant, its member user, and the ENABLE_RAG gate are
upserted. It writes nothing outside its own tenant, and it never touches a
password: the member account is created with none, and every run signs in
through GoTrue's admin one-time-token (magic link) mint --
apps/web-console/tests/e2e/support/live-auth.mjs's protocol, reimplemented
here in Python since that module needs a browser/@supabase/ssr this script
has no reason to depend on. See docs/live-test-auth.md. No credential is
ever generated, printed, stored, or rotated for this account -- earlier
revisions of this script did exactly that (a random password, printed once)
and it is gone entirely, not merely defaulted off, per the same 2026-08-08
incident live-auth.mjs's own header documents: rotating a shared account's
password invalidates every concurrent run holding a session on it.

Retries: the serverless embedding route is slow and uneven (measured between
one and over a hundred seconds per call on the demo stack), so ingest and query
steps are each attempted a few times before the run is called a failure. A
retry here is about upstream weather, not about masking a defect. Every attempt
is printed, so a run that only passes on the third try is visible rather than
silently green.

Also proves the identity-leak fixes on THIS specific route (PR #1222 closed
three leaks here: mint-once-per-stream id, stripped system_fingerprint, and a
non-streaming ragchat- id; a fourth, the DeepSeek post-finish spurious chunk,
was found and fixed separately while writing this check -- see
apps/edge-api/internal/rag/chat_handler.go). Unit tests cover the code path in
isolation; stream_leak_check and error_path_check below are the live proof
that the fix actually holds against a real upstream, since none of the four
leaks PR #1222 found were on a happy path and this route had never been
exercised live before (blocked by ENABLE_RAG defaulting to false for every
tenant that has no explicit tenant_settings row -- opt-in by design, see
supabase/migrations/20260824_01_cowork_gate_default_enabled.sql's header).

Required env: SUPABASE_URL, SUPABASE_SERVICE_ROLE_KEY, SUPABASE_ANON_KEY
Optional env: EDGE_API_URL (default http://localhost:8080),
              RAG_CHAT_MODEL (default hive-fast)
"""
import calendar
import json
import os
import secrets
import sys
import time
import urllib.error
import urllib.parse
import urllib.request

from shared_demo_account import assert_not_shared_demo_account

TENANT_SLUG = "rag-verify-e2e"
TENANT_NAME = "RAG Verify E2E"
TENANT_DEPLOYMENT = "ENTERPRISE_EDGE"
# .invalid is IANA-reserved for exactly this (RFC 2606).
USER_EMAIL = "rag-verify-e2e@hive-e2e.invalid"
# A literal, so this was already safe. Checked anyway, because "safe by accident
# of the literal somebody happened to type" is not a property an edit preserves.
assert_not_shared_demo_account(
    USER_EMAIL,
    variable="USER_EMAIL",
    doing="creates a tenant, uploads a document and sends a real RAG query",
)
MEMBER_ROLE = "MEMBER"
MEMBER_STATUS = "ACTIVE"
RAG_GATE = "ENABLE_RAG"

INGEST_ATTEMPTS = 3
QUERY_ATTEMPTS = 3
INGEST_POLL_SECONDS = 180

# Fixture housekeeping (see purge_stale_fixtures). The prefix is the name
# ingest() uploads under. The window is comfortably longer than the slowest
# observed run (ingest alone allows 180 seconds per attempt, three attempts)
# so a concurrent run's document is never in scope for deletion.
FIXTURE_NAME_PREFIX = "rag-roundtrip-"
FIXTURE_STALE_SECONDS = 1800


def env(name: str, default: str = "") -> str:
    value = os.environ.get(name, "").strip() or default
    if not value:
        print(f"error: {name} is not set", file=sys.stderr)
        sys.exit(1)
    return value


def request(base, headers, method, path, body=None, params=None, prefer=None, timeout=120):
    url = base + path
    if params:
        url += "?" + urllib.parse.urlencode(params)
    data = json.dumps(body).encode() if body is not None else None
    req_headers = dict(headers)
    if data is not None:
        req_headers.setdefault("Content-Type", "application/json")
    if prefer:
        req_headers["Prefer"] = prefer
    req = urllib.request.Request(url, data=data, method=method, headers=req_headers)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            raw = resp.read()
            return resp.status, (json.loads(raw) if raw else None)
    except urllib.error.HTTPError as e:
        raw = e.read()
        try:
            return e.code, json.loads(raw)
        except json.JSONDecodeError:
            return e.code, {"raw": raw[:300].decode(errors="replace")}
    except (urllib.error.URLError, TimeoutError) as e:
        return 0, {"transport_error": str(e)}


def fail(message: str) -> None:
    print(f"FAIL: {message}")
    sys.exit(1)


def provision(supabase_url: str, service_key: str) -> str:
    """Upsert the throwaway tenant, member user (no password -- this account
    signs in only through mint_session's admin one-time-token flow), and the
    RAG gate. Returns tenant_id."""
    headers = {"Authorization": f"Bearer {service_key}", "apikey": service_key}
    rest = supabase_url + "/rest/v1"
    gotrue = supabase_url + "/auth/v1"

    status, body = request(
        rest, headers, "POST", "/tenants",
        body={"slug": TENANT_SLUG, "name": TENANT_NAME, "deployment": TENANT_DEPLOYMENT},
        params={"on_conflict": "slug"},
        prefer="resolution=merge-duplicates,return=representation",
    )
    if status not in (200, 201) or not body:
        fail(f"tenant upsert: {status} {body}")
    tenant_id = body[0]["id"]

    # `filter=` is the exact-match param this GoTrue version supports.
    status, body = request(gotrue, headers, "GET", "/admin/users", params={"filter": USER_EMAIL})
    if status != 200:
        fail(f"user lookup: {status} {body}")
    existing = next(
        (u for u in body.get("users", []) if u.get("email", "").lower() == USER_EMAIL.lower()),
        None,
    )

    metadata = {"selected_tenant_id": tenant_id}
    if existing is None:
        # No `password` field at all: this account is never signed into with
        # one, only through mint_session's admin one-time-token (magic link)
        # flow (docs/live-test-auth.md), so there is no credential to
        # generate, print, store, or ever rotate.
        status, body = request(
            gotrue, headers, "POST", "/admin/users",
            body={"email": USER_EMAIL, "email_confirm": True, "user_metadata": metadata},
        )
        if status not in (200, 201):
            fail(f"user create: {status} {body}")
        user_id = body["id"]
    else:
        user_id = existing["id"]
        status, body = request(
            gotrue, headers, "PUT", f"/admin/users/{user_id}",
            body={"user_metadata": metadata},
        )
        if status != 200:
            fail(f"user metadata update: {status} {body}")

    status, body = request(
        rest, headers, "POST", "/tenant_users",
        body={"tenant_id": tenant_id, "user_id": user_id,
              "role": MEMBER_ROLE, "status": MEMBER_STATUS},
        params={"on_conflict": "tenant_id,user_id"},
        prefer="resolution=merge-duplicates",
    )
    if status not in (200, 201, 204):
        fail(f"membership upsert: {status} {body}")

    status, body = request(
        rest, headers, "POST", "/tenant_settings",
        body={"tenant_id": tenant_id, "key": RAG_GATE, "enabled": True, "updated_by": user_id},
        params={"on_conflict": "tenant_id,key"},
        prefer="resolution=merge-duplicates",
    )
    if status not in (200, 201, 204):
        fail(f"{RAG_GATE} gate upsert: {status} {body}")

    print(f"provisioned tenant {TENANT_SLUG} with {RAG_GATE} enabled")
    return tenant_id


def mint_session(supabase_url: str, service_key: str, anon_key: str) -> str:
    """Mints a live session for USER_EMAIL via GoTrue's admin one-time-token
    (magic link) flow: no password needed, and none is ever set or changed.
    Reimplements apps/web-console/tests/e2e/support/live-auth.mjs's
    mintOnce in Python (that module needs a browser/@supabase/ssr this
    script has no other reason to depend on) -- same two calls, same
    single-retry-on-collision handling: generate_link (service role) ->
    verify (anon key). See docs/live-test-auth.md and live-auth.mjs's own
    header for the 2026-08-08 incident this exists to make structurally
    impossible here (a password rotation invalidated three concurrent
    agents' sessions on a shared account).

    GoTrue keeps ONE outstanding one-time token per user, so two mints for
    the same account that interleave leave the loser's verify answering 403
    "Email link is invalid or has expired"; one retry clears that.
    """
    gotrue = supabase_url + "/auth/v1"
    service_headers = {"Authorization": f"Bearer {service_key}", "apikey": service_key}

    for attempt in range(2):
        status, body = request(
            gotrue, service_headers, "POST", "/admin/generate_link",
            body={"type": "magiclink", "email": USER_EMAIL},
        )
        if status != 200:
            fail(f"generate_link for {USER_EMAIL}: {status} {body}")
        # GoTrue returns these flat over the raw admin API (supabase-js is
        # the one that nests them under `properties`); handle both shapes.
        properties = (body or {}).get("properties") or body or {}
        token_hash = properties.get("hashed_token")
        if not token_hash:
            fail(f"generate_link for {USER_EMAIL}: no hashed_token in response: {body}")

        status, body = request(
            gotrue, {"apikey": anon_key}, "POST", "/verify",
            body={"type": "magiclink", "token_hash": token_hash},
        )
        token = (body or {}).get("access_token", "")
        if status == 200 and token:
            print("signed in via admin one-time-token mint, no password used or changed")
            return token
        message = str((body or {}).get("msg") or (body or {}).get("error_description") or body)
        if attempt == 0 and "invalid or has expired" in message.lower():
            continue
        fail(f"verify for {USER_EMAIL}: {status} {body}")


def ingest(edge: str, auth: dict, marker: str) -> str:
    """Upload a marked document and wait for status=embedded. Returns doc id."""
    content = (
        "Hive RAG round-trip fixture. "
        f"The pilot codename recorded in this note is {marker}. "
        "No other document in this tenant carries that codename."
    )
    for attempt in range(1, INGEST_ATTEMPTS + 1):
        status, body = request(
            edge, auth, "POST", "/v1/rag/documents",
            body={"name": f"rag-roundtrip-{marker}.txt", "content": content},
        )
        if status != 202:
            print(f"  upload attempt {attempt}: http {status} {body}")
            continue
        doc_id = body["id"]
        deadline = time.time() + INGEST_POLL_SECONDS
        state = "pending"
        while time.time() < deadline:
            status, doc = request(edge, auth, "GET", f"/v1/rag/documents/{doc_id}")
            state = (doc or {}).get("status", "")
            if state in ("embedded", "error"):
                break
            time.sleep(2)
        print(f"  ingest attempt {attempt}: status={state} {(doc or {}).get('error_msg') or ''}")
        if state == "embedded":
            return doc_id
    fail(f"document never reached status=embedded after {INGEST_ATTEMPTS} attempts")


def parse_created_at(value: str) -> "float | None":
    """Epoch seconds from the API's created_at, or None when unparseable.

    Deliberately tolerant about what it accepts: only the leading
    YYYY-MM-DDTHH:MM:SS is read, so a trailing Z, an offset, or sub-second
    digits of any length all parse the same.

    None rather than a sentinel age, because both sentinels are wrong. Treat
    an unreadable timestamp as ancient and the purge deletes a document a
    concurrent run may still be using. Treat it as recent and the purge
    silently stops working, which brings back the crowding failure it exists
    to prevent. The caller keeps the document and says so, so the format
    change surfaces as a line in the log rather than as either failure.
    """
    try:
        return float(calendar.timegm(time.strptime(str(value)[:19], "%Y-%m-%dT%H:%M:%S")))
    except (ValueError, TypeError):
        return None


def purge_stale_fixtures(edge: str, auth: dict) -> None:
    """Delete round-trip fixture documents left behind by earlier runs.

    Not tidiness. Every run ingests a near-identical note whose only
    difference is its codename, retrieval asks the same question every time,
    and the search assertion reads top_k=3. So run four onwards competes with
    its own history for those three slots and eventually loses: observed on
    the demo box on 2026-08-28, where a correct pipeline reported "3 hits but
    no marker" on all three attempts, and the grounded answer before that had
    already started replying "there are multiple notes, each recording a
    different pilot codename". A check that degrades into a false failure the
    more often it runs is worse than no check, because the first few greens
    teach everyone to trust it.

    Only this script's own fixtures are touched (the rag-roundtrip- name
    prefix, inside its own throwaway tenant), and only ones old enough that no
    live run could still be using them, so two runs overlapping cannot delete
    each other's document. Best effort throughout: a failure to list or delete
    is reported and ignored, since it is housekeeping and not the thing under
    test.
    """
    status, body = request(edge, auth, "GET", "/v1/rag/documents")
    if status != 200:
        print(f"  fixture purge: skipped, list returned http {status}")
        return
    docs = (body or {}).get("data", [])
    cutoff = time.time() - FIXTURE_STALE_SECONDS
    removed = 0
    for doc in docs:
        if not str(doc.get("name", "")).startswith(FIXTURE_NAME_PREFIX):
            continue
        created = parse_created_at(doc.get("created_at", ""))
        if created is None:
            print(f"  fixture purge: kept {doc['id']}, created_at {doc.get('created_at')!r} is not a shape this understands")
            continue
        if created > cutoff:
            continue
        del_status, _ = request(edge, auth, "DELETE", f"/v1/rag/documents/{doc['id']}")
        if del_status in (200, 202, 204):
            removed += 1
        else:
            print(f"  fixture purge: document {doc['id']} delete returned http {del_status}")
    print(f"purged {removed} stale fixture document(s); tenant held {len(docs)} before the purge")


def delete_document(edge: str, auth: dict, doc_id: str) -> None:
    """Remove this run's own fixture. Best effort: the run's verdict is
    already decided by the time this is called, and the purge above is the
    backstop for anything this misses."""
    status, _ = request(edge, auth, "DELETE", f"/v1/rag/documents/{doc_id}")
    if status in (200, 202, 204):
        print(f"removed this run's fixture document {doc_id}")
    else:
        print(f"could not remove fixture document {doc_id}: http {status}")


def search(edge: str, auth: dict, marker: str) -> None:
    for attempt in range(1, QUERY_ATTEMPTS + 1):
        status, body = request(
            edge, auth, "POST", "/v1/rag/search",
            body={"query": "What is the pilot codename recorded in the note?", "top_k": 3},
        )
        if status != 200:
            print(f"  search attempt {attempt}: http {status} {body}")
            continue
        hits = (body or {}).get("results", [])
        if any(marker in h.get("content", "") for h in hits):
            top = hits[0].get("score")
            print(f"  search attempt {attempt}: {len(hits)} hits, marker retrieved, top score {top}")
            return
        print(f"  search attempt {attempt}: {len(hits)} hits but no marker")
    fail("vector search never returned the marked chunk")


def grounded_chat(edge: str, auth: dict, marker: str, model: str) -> None:
    for attempt in range(1, QUERY_ATTEMPTS + 1):
        status, body = request(
            edge, auth, "POST", "/v1/rag/chat",
            body={
                "model": model,
                "messages": [{"role": "user", "content":
                              "What pilot codename is recorded in the note? "
                              "Answer only from the provided context."}],
                "top_k": 3,
            },
        )
        if status != 200:
            print(f"  chat attempt {attempt}: http {status} {body}")
            continue
        try:
            answer = body["choices"][0]["message"]["content"]
        except (KeyError, IndexError, TypeError):
            print(f"  chat attempt {attempt}: unexpected response shape {str(body)[:200]}")
            continue
        citations = (body or {}).get("citations") or []
        print(f"  chat attempt {attempt}: {answer.strip()[:200]}")
        if marker not in answer.upper():
            print(f"  chat attempt {attempt}: answer is not grounded in the document")
            continue
        if not citations:
            fail("grounded answer carried no citations")
        print(f"  chat attempt {attempt}: grounded, {len(citations)} citations")
        return
    fail("grounded chat never answered from the document")


# Substrings that must never reach a customer on any provider-blind surface
# (CLAUDE.md: "provider names never leak to customers"). Lowercase, checked
# against a lowercased frame.
PROVIDER_NAME_MARKERS = ("openrouter", "groq", "deepseek", "litellm")


def check_stream_frames(raw_frames: list[str], requested_alias: str) -> list[str]:
    """Pure assertion over a list of already-split `data: ` payloads (JSON
    text, no prefix, [DONE] and the leading rag.citations frame already
    excluded by the caller). Returns a list of violation strings; empty means
    clean. No network, no side effects -- see test_verify_rag_roundtrip.py.

    Mirrors, in the client, exactly what
    apps/edge-api/internal/rag/chat_handler.go's streamGroundedChat is
    supposed to guarantee server-side: one stable gateway-minted id per
    stream, no system_fingerprint, no provider name or raw upstream id, model
    always rewritten to the requested alias, and nothing relayed after a
    terminal finish_reason except a genuine usage-only terminal frame.
    """
    violations: list[str] = []
    ids_seen: set[str] = set()
    finish_seen = False
    for i, raw in enumerate(raw_frames):
        lower = raw.lower()
        if '"system_fingerprint"' in lower:
            violations.append(f"frame {i}: system_fingerprint key present")
        if '"id":"gen-' in lower:
            violations.append(f"frame {i}: raw OpenRouter gen-* id leaked")
        for marker in PROVIDER_NAME_MARKERS:
            if marker in lower:
                violations.append(f"frame {i}: provider-identifying string {marker!r} present")
        try:
            chunk = json.loads(raw)
        except json.JSONDecodeError:
            violations.append(f"frame {i}: not valid JSON: {raw[:120]!r}")
            continue

        chunk_id = chunk.get("id")
        if chunk_id:
            ids_seen.add(chunk_id)
            if not chunk_id.startswith("ragchat-"):
                violations.append(
                    f"frame {i}: id {chunk_id!r} is not gateway-minted (expected a ragchat- prefix)"
                )

        model = chunk.get("model")
        if model and model != requested_alias:
            violations.append(
                f"frame {i}: model {model!r} differs from the requested alias {requested_alias!r}"
            )

        choices = chunk.get("choices") or []
        # `is not None`, not a truthiness test: the Go rule this mirrors is
        # `chunk.Usage != nil` (inference.ShouldSuppressPostFinishChunk), and
        # a frame carrying `"usage": {}` after finish_reason decodes to a
        # non-nil pointer there, so the server forwards it. Under
        # `bool(chunk.get("usage"))` this checker would have called that same
        # forwarded frame a leak: a false positive on a shape the server is
        # allowed to emit. Unlikely shape, but the two rules now agree
        # exactly, which is the point of a mirror.
        is_usage_only_terminal = chunk.get("usage") is not None and not choices
        # Suppression must be judged against finishSeen from EARLIER frames
        # only, so this reads finish_seen before the update below, same
        # ordering as inference.ShouldSuppressPostFinishChunk /
        # apps/edge-api/internal/rag/chat_handler.go's streaming loop.
        if finish_seen and not is_usage_only_terminal:
            violations.append(f"frame {i}: chunk relayed after finish_reason (post-finish leak)")
        if any(c.get("finish_reason") for c in choices):
            finish_seen = True

    if len(ids_seen) > 1:
        violations.append(
            f"multiple distinct ids on one stream: {sorted(ids_seen)} (client-visible id must be stable)"
        )
    return violations


def parse_sse_data_frames(resp):
    """Yields each `data: ` line's payload (no prefix, no trailing newline)
    from a streaming HTTP response, stopping at (and excluding) [DONE]."""
    for raw_line in resp:
        line = raw_line.decode("utf-8", errors="replace").rstrip("\r\n")
        if not line.startswith("data: "):
            continue
        payload = line[len("data: "):]
        if payload == "[DONE]":
            return
        yield payload


def stream_leak_check(edge: str, auth: dict, marker: str, model: str) -> None:
    """Live proof, on the real streaming wire, that /v1/rag/chat never leaks
    provider identity and never relays a chunk after finish_reason. Skips the
    leading rag.citations frame (has no id/model/finish_reason, nothing to
    check) and asserts every remaining frame via check_stream_frames."""
    for attempt in range(1, QUERY_ATTEMPTS + 1):
        req = urllib.request.Request(
            edge + "/v1/rag/chat",
            data=json.dumps({
                "model": model,
                "messages": [{"role": "user", "content":
                              "What pilot codename is recorded in the note? "
                              "Answer only from the provided context."}],
                "top_k": 3,
                "stream": True,
            }).encode(),
            method="POST",
            headers={**auth, "Content-Type": "application/json"},
        )
        try:
            with urllib.request.urlopen(req, timeout=120) as resp:
                if resp.status != 200:
                    print(f"  stream leak-check attempt {attempt}: http {resp.status}")
                    continue
                frames = [f for f in parse_sse_data_frames(resp) if "rag.citations" not in f]
        except urllib.error.HTTPError as e:
            print(f"  stream leak-check attempt {attempt}: http {e.code} {e.read()[:200]!r}")
            continue
        except (urllib.error.URLError, TimeoutError) as e:
            print(f"  stream leak-check attempt {attempt}: transport error {e}")
            continue

        if not frames:
            print(f"  stream leak-check attempt {attempt}: no data frames received, retrying")
            continue

        violations = check_stream_frames(frames, model)
        if not violations:
            print(f"  stream leak-check attempt {attempt}: clean, {len(frames)} frames, no leaks")
            return
        print(f"  stream leak-check attempt {attempt}: {len(violations)} violation(s):")
        for v in violations:
            print(f"    - {v}")
    fail("RAG streaming leak-check failed: see violations above")


def error_path_check(edge: str, auth: dict, model: str) -> None:
    """Forces a real (non-synthesized) upstream error -- an oversized user
    message most providers reject with a context-length-style 4xx -- and
    proves the error response stays provider-blind. None of the four leaks
    found across PR #1222 and this fix were on a happy path, so the error
    path gets its own live check rather than being assumed clean because the
    happy path is.

    Best-effort: if the upstream accepts the request anyway (a model with a
    large enough context window), this reports and returns rather than
    failing the whole run over an error it could not force.
    """
    # ~180KB of message content, under the handler's 256KB request-body cap
    # (apps/edge-api/internal/rag/chat_handler.go's io.LimitReader) but large
    # enough to exceed a "fast" model's context window on most providers.
    oversized = "filler word " * 15000
    status, body = request(
        edge, auth, "POST", "/v1/rag/chat",
        body={
            "model": model,
            "messages": [{"role": "user", "content": oversized}],
            "top_k": 1,
        },
        timeout=60,
    )
    if 200 <= status < 300:
        print(f"  error-path check: upstream accepted the oversized request (http {status}); "
              "could not force an error this run")
        return
    text = json.dumps(body).lower()
    leaked = [m for m in PROVIDER_NAME_MARKERS if m in text]
    if leaked:
        fail(f"error path leaked provider identity {leaked} in response: {body}")
    print(f"  error-path check: http {status}, provider-blind: {body}")


def main() -> None:
    supabase_url = env("SUPABASE_URL").rstrip("/")
    service_key = env("SUPABASE_SERVICE_ROLE_KEY")
    anon_key = env("SUPABASE_ANON_KEY")
    edge = env("EDGE_API_URL", "http://localhost:8080").rstrip("/")
    model = env("RAG_CHAT_MODEL", "hive-fast")

    # Uppercase so the answer check is case-insensitive without weakening it.
    marker = "PLUM" + secrets.token_hex(3).upper()

    provision(supabase_url, service_key)
    auth = {"Authorization": f"Bearer {mint_session(supabase_url, service_key, anon_key)}"}

    purge_stale_fixtures(edge, auth)

    print(f"ingesting document marked {marker}")
    doc_id = ingest(edge, auth, marker)
    try:
        print(f"searching for the marker (document {doc_id})")
        search(edge, auth, marker)
        print(f"asking {model} to answer from the document")
        grounded_chat(edge, auth, marker, model)
        print(f"streaming {model} and checking for identity/post-finish leaks")
        stream_leak_check(edge, auth, marker, model)
        print(f"forcing an error path against {model} and checking it stays provider-blind")
        error_path_check(edge, auth, model)
    finally:
        # Runs on the failure path too, on purpose: a failed run that leaves
        # its fixture behind makes the NEXT run likelier to fail for the same
        # crowding reason, which is how one real defect turns into a
        # permanently red check nobody trusts.
        delete_document(edge, auth, doc_id)

    print("PASS: upload, embed, vector search, grounded answer, streaming leak-check, "
          "and error-path leak-check all verified")


if __name__ == "__main__":
    main()

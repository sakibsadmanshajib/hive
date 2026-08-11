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
upserted. It writes nothing outside its own tenant, and it does not rotate the
member user's password. It used to, on every run, against a hardcoded shared
address, which is the shape docs/live-test-auth.md forbids: the control-plane
resolves every bearer against GoTrue per request, so a rotation revokes the
sessions of every other run holding one. A first run creates the account and
prints the password it generated, on stderr, once. Save that value: every later
run signs in with it through RAG_VERIFY_PASSWORD, and no run will rotate it.

Retries: the serverless embedding route is slow and uneven (measured between
one and over a hundred seconds per call on the demo stack), so ingest and query
steps are each attempted a few times before the run is called a failure. A
retry here is about upstream weather, not about masking a defect. Every attempt
is printed, so a run that only passes on the third try is visible rather than
silently green.

Required env: SUPABASE_URL, SUPABASE_SERVICE_ROLE_KEY, SUPABASE_ANON_KEY, and
              RAG_VERIFY_PASSWORD once the member account exists
Optional env: EDGE_API_URL (default http://localhost:8080),
              RAG_CHAT_MODEL (default hive-fast)
"""
import json
import os
import secrets
import string
import sys
import time
import urllib.error
import urllib.parse
import urllib.request

TENANT_SLUG = "rag-verify-e2e"
TENANT_NAME = "RAG Verify E2E"
TENANT_DEPLOYMENT = "ENTERPRISE_EDGE"
# .invalid is IANA-reserved for exactly this (RFC 2606).
USER_EMAIL = "rag-verify-e2e@hive-e2e.invalid"
MEMBER_ROLE = "MEMBER"
MEMBER_STATUS = "ACTIVE"
RAG_GATE = "ENABLE_RAG"

INGEST_ATTEMPTS = 3
QUERY_ATTEMPTS = 3
INGEST_POLL_SECONDS = 180


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


def random_password() -> str:
    alphabet = string.ascii_letters + string.digits + "!@#$%^&*-_"
    return "Aa1!" + "".join(secrets.choice(alphabet) for _ in range(24))


def fail(message: str) -> None:
    print(f"FAIL: {message}")
    sys.exit(1)


def provision(supabase_url: str, service_key: str) -> tuple[str, str]:
    """Upsert the throwaway tenant, member user, and RAG gate. Returns
    (tenant_id, password)."""
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

    # An existing account keeps its password. This script signs in with a
    # password, so it needs one, but overwriting the account's would revoke
    # every session another run holds on it (docs/live-test-auth.md), and
    # this address is hardcoded and shared. So the value comes from the
    # caller, and its absence is a loud failure naming the variable rather
    # than a rotation nobody asked for. A brand-new account has no session to
    # break, so it still gets a fresh random one.
    env_password = os.environ.get("RAG_VERIFY_PASSWORD", "").strip()
    metadata = {"selected_tenant_id": tenant_id}
    if existing is None:
        password = env_password or random_password()
        status, body = request(
            gotrue, headers, "POST", "/admin/users",
            body={"email": USER_EMAIL, "password": password,
                  "email_confirm": True, "user_metadata": metadata},
        )
        if status not in (200, 201):
            fail(f"user create: {status} {body}")
        user_id = body["id"]
        if not env_password:
            # Print it, once, on the only run that can know it. Without this
            # the account exists with a password nobody holds, and every later
            # run fails on the branch below demanding a value no operator could
            # ever obtain, which would leave DEMO.md's RAG proof permanently
            # dead on any environment this has already run against.
            print(
                f"created {USER_EMAIL} with a fresh password. Save it: every later "
                f"run needs it, and no run will rotate it.\n"
                f"  export RAG_VERIFY_PASSWORD={password}",
                file=sys.stderr,
            )
    else:
        if not env_password:
            fail(
                f"{USER_EMAIL} already exists and this script will not rotate its "
                "password: doing so revokes every session any concurrent run holds "
                "on it (docs/live-test-auth.md). Set RAG_VERIFY_PASSWORD to the "
                "account's existing password to sign in with it."
            )
        password = env_password
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
    return tenant_id, password


def sign_in(supabase_url: str, anon_key: str, password: str) -> str:
    status, body = request(
        supabase_url + "/auth/v1", {"apikey": anon_key}, "POST",
        "/token?grant_type=password", body={"email": USER_EMAIL, "password": password},
    )
    token = (body or {}).get("access_token", "")
    if status != 200 or not token:
        fail(f"sign in: {status} {body}")
    print("signed in, tenant-scoped JWT acquired")
    return token


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


def main() -> None:
    supabase_url = env("SUPABASE_URL").rstrip("/")
    service_key = env("SUPABASE_SERVICE_ROLE_KEY")
    anon_key = env("SUPABASE_ANON_KEY")
    edge = env("EDGE_API_URL", "http://localhost:8080").rstrip("/")
    model = env("RAG_CHAT_MODEL", "hive-fast")

    # Uppercase so the answer check is case-insensitive without weakening it.
    marker = "PLUM" + secrets.token_hex(3).upper()

    _, password = provision(supabase_url, service_key)
    auth = {"Authorization": f"Bearer {sign_in(supabase_url, anon_key, password)}"}

    print(f"ingesting document marked {marker}")
    doc_id = ingest(edge, auth, marker)
    print(f"searching for the marker (document {doc_id})")
    search(edge, auth, marker)
    print(f"asking {model} to answer from the document")
    grounded_chat(edge, auth, marker, model)

    print("PASS: upload, embed, vector search, and grounded answer all verified")


if __name__ == "__main__":
    main()

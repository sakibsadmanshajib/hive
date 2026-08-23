#!/usr/bin/env python3
"""Assert the deployed product still works, after a deploy has already exited 0.

A deploy exiting zero is not the claim "the product works". PR #787 merged
green and took chat sign-in down. Three consecutive deploys failed at `migrate`
and the box silently stayed on old code. This script is the gate that runs
after the deploy and answers, against the live hostnames, the only three
questions a demo actually depends on:

  1. Chat answers.        The chat backend is up, not merely the static shell.
  2. Sign-in completes.   A minted session resolves to the same user at the
                          control-plane, per request, against live GoTrue.
  3. A ledger entry lands. A real completion produces a real `usage_charge`
                          row, so billing has not failed open.

Each check is written to fail for the reason it names and for no other, and
each has a negative control (see --negative-control) that makes it genuinely
red on demand, so the gate can prove it still bites without anyone editing it.

Nothing here rolls anything back. Forward migrations run before the deploy and
are not reversible by redeploying code, so an automatic rollback would leave
old code against a new schema, which is worse than a bad deploy. This script
fails loudly; a human decides what to do about it.

Usage:

    set -a; . .env; set +a
    python3 scripts/post-deploy-verify.py

Required env:
    SUPABASE_URL, SUPABASE_ANON_KEY   the auth origin the deployment uses.
                                      This must be the PUBLIC origin, the one a
                                      browser signs in against, not the
                                      in-network compose hostname the services
                                      talk to each other on. On the demo box
                                      those are two different values
                                      (NEXT_PUBLIC_SUPABASE_URL and
                                      SUPABASE_URL respectively), and the
                                      in-network one does not resolve outside
                                      the compose network at all.

    HIVE_CHAT_URL,                    the public origins this deployment is
    HIVE_CONTROL_PLANE_URL,           supposed to serve. All three come from
    HIVE_EDGE_API_URL                 the environment and none has a default,
                                      and this is deliberate rather than
                                      pedantry. A checker that silently falls
                                      back to a fixed demo host can return a
                                      green result for a product nobody
                                      deployed: the target must be named by
                                      whoever owns the topology, and a missing
                                      name stops the run before any request.

Required env for the `signin` and `ledger` checks only:
    HIVE_VERIFY_EMAIL                 a dedicated verification identity
    HIVE_VERIFY_PASSWORD              its existing password

    The `chat` check needs neither, so `--only chat` runs with no identity at
    all. That is not a convenience: it means a deployment with no verification
    identity configured yet still gets its chat backend checked, instead of the
    whole gate going dark on a missing credential.

Optional env:
    HIVE_VERIFY_MODEL          default hive-default
    HIVE_VERIFY_LEDGER_TIMEOUT default 90 (seconds to wait for the charge)
    HIVE_VERIFY_LEDGER_PAGES   default 20 (page cap on the ledger scan)
    HIVE_VERIFY_RUN_LABEL      free-text tag for the minted key's nickname

Check 3 is a REAL SPEND on a real identity. HIVE_VERIFY_EMAIL has no default
and must never be demo@hive-demo.invalid (issue #848, docs/live-test-auth.md).
It must be a tenant OWNER with a verified email: minting an API key needs
api_keys:write, which the authorization policy grants to OWNER only.

This script signs in with an existing password and NEVER rotates one. The
control-plane resolves every bearer against GoTrue per request, so a rotation
revokes every concurrent session; that broke three agents at once on
2026-08-08. There is no code path here that writes a password.

Nothing secret is ever printed. Tokens, keys and passwords are reported by
presence and length only, and this repository is public.
"""
import argparse
import base64
import json
import os
import sys
import time
import urllib.error
import urllib.request
import uuid

CHECKS = ("chat", "signin", "ledger")

# Every request this script sends carries an explicit User-Agent, and that is
# load bearing rather than politeness. Both demo hostnames sit behind
# Cloudflare, whose bot rules answer 403 "Error 1010: Access denied ... based on
# your browser's signature" to the literal default urllib sends
# (`Python-urllib/3.x`). Measured, not guessed: the same GET of
# https://chat-hive.scubed.co/api/config returns 403 with that default and 200
# with the string below, from the same host in the same second. Without this the
# gate would have gone red on every run, blaming a dead chat backend for a
# blocked user agent, which is exactly the "fails for a reason it does not name"
# outcome the checks are written to avoid.
USER_AGENT = "hive-post-deploy-verify/1 (+https://github.com/sakibsadmanshajib/hive)"

# Verification targets. Resolved in main() from the environment and nothing
# else, before any request is sent: see the Required env note in the module
# docstring for why none of these carries a default.
CHAT = CP = EDGE = ""
MODEL = os.environ.get("HIVE_VERIFY_MODEL", "hive-default").strip() or "hive-default"
LEDGER_TIMEOUT = float(os.environ.get("HIVE_VERIFY_LEDGER_TIMEOUT", "90"))
LEDGER_PAGES = int(os.environ.get("HIVE_VERIFY_LEDGER_PAGES", "20"))
LEDGER_PAGE_SIZE = 500
RUN_LABEL = os.environ.get("HIVE_VERIFY_RUN_LABEL", "").strip() or "local"


class CheckFailed(Exception):
    """One check's assertion did not hold. Carries the operator-facing reason."""


def env(name: str) -> str:
    value = os.environ.get(name, "").strip()
    if not value:
        raise SystemExit(f"error: {name} is not set")
    return value


def http(method, url, headers=None, body=None, timeout=120):
    data = json.dumps(body).encode() if body is not None else None
    sent = dict(headers or {})
    sent.setdefault("User-Agent", USER_AGENT)
    req = urllib.request.Request(url, data=data, method=method, headers=sent)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as r:
            return r.status, r.read().decode(errors="replace")
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode(errors="replace")
    except Exception as e:  # noqa: BLE001 - a transport failure is a result too
        return 0, f"<transport error: {type(e).__name__}: {e}>"


def snippet(raw: str, limit: int = 240) -> str:
    return " ".join(raw.split())[:limit]


# ── check 1: chat answers ────────────────────────────────────────────────────

def check_chat(negative: bool) -> None:
    """Assert the chat BACKEND answers, not merely that a page is served.

    The trap this exists to avoid: chat-hive.scubed.co serves its static shell
    from disk and returns a perfectly good 200 with a full HTML page even when
    the backend behind it is dead. Any check that asserts a status code, a page
    title, or the presence of markup passes straight through an outage. So this
    asks the backend a question only the backend can answer: /api/config is
    rendered by Open WebUI's Python process from live configuration, and it
    carries `status: true` plus the OIDC provider entry that the whole
    Hive-account sign-in flow depends on. A shell with no backend cannot
    produce either.

    Negative control: read the static shell at / instead. That is the exact
    "merely returning a page" trap, and it must fail here.
    """
    path = "/" if negative else "/api/config"
    if negative:
        print("  negative control: reading the static shell instead of the backend config")
    status, raw = http("GET", CHAT + path, {"Accept": "application/json"}, timeout=30)
    print(f"  GET {CHAT}{path} -> {status}")
    if status != 200:
        raise CheckFailed(f"chat {path} answered {status}, expected 200: {snippet(raw)}")

    try:
        config = json.loads(raw)
    except json.JSONDecodeError:
        raise CheckFailed(
            f"chat {path} answered 200 but the body is not JSON, so this is the static "
            f"shell rather than the backend: {snippet(raw, 120)}"
        ) from None
    if not isinstance(config, dict):
        raise CheckFailed(f"chat {path} returned JSON that is not an object: {snippet(raw, 120)}")

    if config.get("status") is not True:
        raise CheckFailed(
            f"chat config carries status={config.get('status')!r}, expected true. "
            "The backend is answering but reports itself unhealthy."
        )
    print("  status: true")

    providers = (config.get("oauth") or {}).get("providers") or {}
    if not isinstance(providers, dict) or "oidc" not in providers:
        raise CheckFailed(
            "chat config carries no oauth.providers.oidc entry, so the Hive-account "
            f"sign-in button is absent from the login page. providers={sorted(providers) if isinstance(providers, dict) else providers!r}"
        )
    print(f"  oauth.providers.oidc present ({providers['oidc']!r})")


# ── check 2: sign-in completes ───────────────────────────────────────────────

def mint_session(supabase: str, anon: str, email: str, password: str) -> tuple[str, str]:
    """Sign in with an existing password. Returns (access_token, user_id).

    Password grant on the public listener, deliberately: it is the flow a real
    customer uses, it needs no admin route, and so it is unaffected by the
    gateway's admin deny list. Nothing here writes a password.
    """
    status, raw = http(
        "POST", f"{supabase}/auth/v1/token?grant_type=password",
        {"apikey": anon, "Content-Type": "application/json"},
        {"email": email, "password": password},
        timeout=60,
    )
    if status != 200:
        raise CheckFailed(f"sign-in against {supabase} answered {status}: {snippet(raw)}")
    try:
        payload = json.loads(raw)
    except json.JSONDecodeError:
        raise CheckFailed("sign-in answered 200 with a body that is not JSON") from None
    if not isinstance(payload, dict):
        raise CheckFailed(
            "sign-in answered 200 with JSON that is not an object "
            f"({type(payload).__name__}), so no access_token can be read"
        )
    user = payload.get("user")
    if user is not None and not isinstance(user, dict):
        raise CheckFailed(
            f"sign-in answered 200 but its user field is {type(user).__name__}, not an object"
        )
    token = payload.get("access_token") or ""
    user_id = (user or {}).get("id") or ""
    if not token:
        raise CheckFailed("sign-in answered 200 but returned no access_token")
    if not user_id:
        raise CheckFailed("sign-in answered 200 but returned no user id")
    print(f"  minted a session, token {len(token)} characters, user {user_id}")
    return token, user_id


def check_signin(token: str, user_id: str, negative: bool) -> None:
    """Assert the control-plane resolves this bearer to this same user.

    A rendered login page proves nothing: the shell renders before any token is
    validated. The control-plane's auth middleware, by contrast, looks the
    bearer up against live GoTrue on every single request, so a 200 from
    /api/v1/viewer carrying the same user id the sign-in minted is evidence the
    whole chain resolved: GoTrue issued it, the control-plane accepted it, and
    it denotes the identity we think it does.

    Comparing the id, not just the status, is the part that matters. A 200 with
    somebody else's identity is a worse outcome than a 401 and would otherwise
    read as a pass.

    Negative control: corrupt the bearer's signature bytes. A control-plane that
    genuinely revalidates must reject it; one that trusts a cached or rendered
    session would not.
    """
    bearer = token
    if negative:
        print("  negative control: corrupting the bearer's signature bytes")
        parts = token.split(".")
        if len(parts) == 3 and parts[2]:
            head, claims, sig = parts
            # Swapping the final base64url CHARACTER is not enough: that
            # character can carry unused low bits, so A->B there can decode to
            # identical bytes and the control would send the original token and
            # pass. Mutating DECODED bytes guarantees the signature the server
            # sees differs, while header and payload stay intact, so a
            # rejection can only be signature validation rather than a parse
            # error.
            decoded = bytearray(base64.urlsafe_b64decode(sig + "=" * (-len(sig) % 4)))
            decoded[0] ^= 0xFF
            bad_sig = base64.urlsafe_b64encode(bytes(decoded)).decode().rstrip("=")
            bearer = ".".join((head, claims, bad_sig))
        else:
            # Not a three-segment JWT. Rejection is still the point; corrupt it
            # wholesale instead of crashing on an unpack.
            bearer = ("X" if not token.startswith("X") else "Y") + token[1:]

    status, raw = http("GET", f"{CP}/api/v1/viewer",
                       {"Authorization": f"Bearer {bearer}"}, timeout=60)
    print(f"  GET {CP}/api/v1/viewer -> {status}")
    if status != 200:
        raise CheckFailed(f"/api/v1/viewer answered {status}, expected 200: {snippet(raw)}")
    try:
        viewer = json.loads(raw)
    except json.JSONDecodeError:
        raise CheckFailed("/api/v1/viewer answered 200 with a body that is not JSON") from None
    if not isinstance(viewer, dict):
        raise CheckFailed(
            "/api/v1/viewer answered 200 with JSON that is not an object "
            f"({type(viewer).__name__})"
        )
    user = viewer.get("user")
    if user is not None and not isinstance(user, dict):
        raise CheckFailed(
            f"/api/v1/viewer returned a user field that is {type(user).__name__}, not an object"
        )

    seen = (user or {}).get("id") or ""
    if seen != user_id:
        raise CheckFailed(
            f"/api/v1/viewer resolved the bearer to user {seen!r}, but the session was "
            f"minted for {user_id!r}. The control-plane is serving the wrong identity."
        )
    print(f"  control-plane resolved the bearer to the same user {seen}")


# ── check 3: a ledger entry lands ────────────────────────────────────────────

def scan_usage_charges(auth: dict, stop_when_new_of: set[str] | None = None) -> tuple[set[str], dict | None]:
    """Collect every usage_charge id on the current account.

    A full scan, and not a peek at the first page, because the ledger's list
    query is `ORDER BY id DESC` over a random UUIDv4 primary key
    (apps/control-plane/internal/ledger/repository.go). That is a total order,
    so keyset pagination over it is complete and stable, but it is NOT
    chronological: a row written a second ago lands at a uniformly random
    position. Reading page one and looking for something new finds it only by
    luck, which is a check that passes at random.

    When `stop_when_new_of` is given, the scan returns as soon as it finds an id
    outside that set, so the common case costs one page.
    """
    seen: set[str] = set()
    cursor = None
    for page in range(LEDGER_PAGES):
        query = f"?type=usage_charge&limit={LEDGER_PAGE_SIZE}"
        if cursor:
            query += f"&cursor={cursor}"
        status, raw = http("GET", f"{CP}/api/v1/accounts/current/credits/ledger{query}",
                           auth, timeout=60)
        if status != 200:
            raise CheckFailed(f"ledger read answered {status}, expected 200: {snippet(raw)}")
        try:
            body = json.loads(raw)
        except json.JSONDecodeError:
            raise CheckFailed("ledger read answered 200 with a body that is not JSON") from None
        if not isinstance(body, dict):
            raise CheckFailed(
                "ledger read answered 200 with JSON that is not an object "
                f"({type(body).__name__})"
            )
        entries = body.get("entries")
        if not isinstance(entries, list):
            raise CheckFailed(
                f"ledger read answered 200 but carried entries as {type(entries).__name__}, "
                "expected a list"
            )

        for entry in entries:
            # A non-dict row is treated like a row without an id below: it
            # cannot be the new charge, and inventing an id for it would be
            # worse than skipping it.
            if not isinstance(entry, dict):
                continue
            entry_id = str(entry.get("id") or "")
            if not entry_id:
                continue
            seen.add(entry_id)
            if stop_when_new_of is not None and entry_id not in stop_when_new_of:
                return seen, entry

        cursor = body.get("next_cursor")
        if not cursor:
            return seen, None

    # Silence here would mean comparing two partial sets and calling a miss a
    # pass. A page cap that has been reached is a real problem with the
    # verification account, and it says so.
    raise CheckFailed(
        f"the verification account has more than {LEDGER_PAGES * LEDGER_PAGE_SIZE} "
        "usage_charge entries, so this scan cannot enumerate them and cannot tell a new "
        "one from an old one. Rotate the verification identity, or raise "
        "HIVE_VERIFY_LEDGER_PAGES deliberately."
    )


def check_ledger(auth: dict, negative: bool) -> None:
    """Assert a real completion produces a real usage_charge row.

    Billing failing open is invisible from every other angle: the completion
    succeeds, the customer is served, the response is indistinguishable from a
    billed one, and the only trace of the failure is a row that is not there.
    That is what happened for three days in July 2026. So this sends a real
    completion through a real key and requires the charge to appear.

    The key is minted through this session on purpose. A pre-provisioned
    HIVE_API_KEY could belong to a different account than the one whose ledger
    is read here, in which case the charge lands somewhere this check never
    looks and the check goes red for the wrong reason, or worse, an unrelated
    row makes it green. Minting through the session guarantees key and ledger
    share an account.

    Negative control: skip the spend. Nothing bills, no row appears, and the
    check must go red exactly as it would if billing had failed open.
    """
    print("  scanning existing usage_charge entries")
    before, _ = scan_usage_charges(auth)
    print(f"  {len(before)} usage_charge entries already on this account")

    nickname = f"post-deploy-verify {RUN_LABEL} {uuid.uuid4().hex[:8]}"
    status, raw = http("POST", f"{CP}/api/v1/accounts/current/api-keys",
                       auth, {"nickname": nickname}, timeout=60)
    if status != 201:
        raise CheckFailed(f"minting an API key answered {status}, expected 201: {snippet(raw)}")
    try:
        created = json.loads(raw)
    except json.JSONDecodeError:
        # A traceback here would report a crash in the verifier where the truth
        # is a malformed response from the thing being verified, and it would
        # skip the summary that names which check failed.
        raise CheckFailed(
            "minting an API key answered 201 with a body that is not JSON, so the key id "
            "cannot be read and the key cannot be revoked"
        ) from None
    if not isinstance(created, dict):
        raise CheckFailed("the API key create response is JSON but not an object")
    key_id, secret = created.get("id", ""), created.get("secret", "")
    if not key_id or not secret:
        raise CheckFailed("the API key create response carried no id or no secret")
    print(f"  minted API key {key_id}, secret {len(secret)} characters")

    try:
        if negative:
            print("  negative control: skipping the completion, so nothing should bill")
        else:
            key_auth = {"Authorization": f"Bearer {secret}", "Content-Type": "application/json"}
            status, raw = http(
                "POST", f"{EDGE}/v1/chat/completions", key_auth,
                {"model": MODEL,
                 "messages": [{"role": "user", "content": "Reply with the single word: pong."}],
                 "max_tokens": 64},
                timeout=120,
            )
            print(f"  POST {EDGE}/v1/chat/completions -> {status}")
            if status != 200:
                raise CheckFailed(
                    f"the completion answered {status}, expected 200: {snippet(raw)}"
                )

        # The charge is written synchronously, before the response body is
        # flushed (apps/edge-api/internal/inference/orchestrator.go), so the
        # first scan normally finds it. Polling anyway costs nothing on the
        # happy path and covers the streaming settle path, which lands after
        # the last chunk.
        deadline = time.monotonic() + LEDGER_TIMEOUT
        attempt = 0
        while True:
            attempt += 1
            _, found = scan_usage_charges(auth, stop_when_new_of=before)
            if found is not None:
                delta = found.get("credits_delta")
                print(f"  new usage_charge {found.get('id')} credits_delta={delta} "
                      f"created_at={found.get('created_at')}")
                if not isinstance(delta, int) or delta >= 0:
                    raise CheckFailed(
                        f"the new usage_charge entry carries credits_delta={delta!r}, "
                        "which is not a debit. A charge that does not subtract credits "
                        "bills nothing."
                    )
                print(f"  charge landed after {attempt} scan(s)")
                # Break, not return: the revocation-failure verdict after the
                # finally must be reachable on the happy path too.
                break
            if time.monotonic() >= deadline:
                raise CheckFailed(
                    f"no new usage_charge entry appeared within {LEDGER_TIMEOUT:.0f}s of the "
                    "completion. The request was served and nothing was billed, which is "
                    "billing failing open."
                )
            time.sleep(3)
    finally:
        # Always, including on the failure paths above. A key left behind is a
        # live credential on a real account.
        revoke_status, revoke_raw = http(
            "POST", f"{CP}/api/v1/accounts/current/api-keys/{key_id}/revoke",
            auth, {}, timeout=60)
        if revoke_status in (200, 204):
            print(f"  revoked API key {key_id}")
        else:
            print(f"  ::warning::failed to revoke API key {key_id}: "
                  f"{revoke_status} {snippet(revoke_raw, 120)}")

    # An unrevoked key is not a footnote: it is a live credential this run put
    # on a real account, so revocation failing is itself a check failure.
    # Reached only when the body above succeeded; on a failed body the original
    # CheckFailed has already propagated past the finally and remains the
    # reason the check reports, which preserves the earlier verification
    # failure rather than replacing it.
    if revoke_status not in (200, 204):
        raise CheckFailed(
            f"revoking the minted API key {key_id} answered {revoke_status}, expected 200 or "
            "204. The key this run minted stays active on a real account, so the run cannot "
            f"be called clean: {snippet(revoke_raw, 160)}"
        )


# ── driver ───────────────────────────────────────────────────────────────────

def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--only", action="append", choices=CHECKS, default=None,
                        help="run only these checks (repeatable). Default: all three.")
    parser.add_argument("--negative-control", choices=("none",) + CHECKS, default="none",
                        help="deliberately remove the thing one check guards, so that check "
                             "goes red. Proves the gate still bites. This can only cause a "
                             "failure; there is no value of it that makes anything pass.")
    args = parser.parse_args()

    selected = tuple(c for c in CHECKS if c in (args.only or CHECKS))
    negative = args.negative_control
    if negative != "none" and negative not in selected:
        raise SystemExit(f"error: --negative-control {negative} is not among the selected checks")

    # Verification targets first, from the environment and nothing else. A
    # checker that assumes a fixed demo host when configuration is missing can
    # return green for a product nobody deployed, so a missing name stops the
    # run here, before any request.
    global CHAT, CP, EDGE
    CHAT = env("HIVE_CHAT_URL").rstrip("/")
    CP = env("HIVE_CONTROL_PLANE_URL").rstrip("/")
    EDGE = env("HIVE_EDGE_API_URL").rstrip("/")

    supabase = env("SUPABASE_URL").rstrip("/")
    anon = env("SUPABASE_ANON_KEY")

    # Only the checks that sign in need an identity, and demanding one for a
    # run that does not use it would turn "no identity configured" into "chat
    # is not checked either", which is strictly worse information.
    email = password = ""
    if {"signin", "ledger"} & set(selected):
        email = env("HIVE_VERIFY_EMAIL")
        password = env("HIVE_VERIFY_PASSWORD")
        if email == "demo@hive-demo.invalid":
            raise SystemExit(
                "error: HIVE_VERIFY_EMAIL is the shared demo account. This script mints a key "
                "and sends a real completion, and issue #848 exists because that traffic ended "
                "up on the account the owner demos to prospects. Use a dedicated identity."
            )

    print(f"chat {CHAT}")
    print(f"control-plane {CP}")
    print(f"edge-api {EDGE}")
    print(f"auth {supabase}")
    print(f"caller {email or '<none needed for the selected checks>'}")
    print(f"checks {', '.join(selected)}"
          + (f" | negative control on {negative}" if negative != "none" else ""))

    results: dict[str, str] = {}
    session: tuple[str, str] | None = None

    for name in selected:
        print(f"\n== {name} ==")
        try:
            if name == "chat":
                check_chat(negative == "chat")
            elif name == "signin":
                session = mint_session(supabase, anon, email, password)
                check_signin(session[0], session[1], negative == "signin")
            elif name == "ledger":
                if session is None:
                    # The ledger check needs a session of its own when signin
                    # was not selected. Same credentials, same no-rotation rule.
                    session = mint_session(supabase, anon, email, password)
                auth = {"Authorization": f"Bearer {session[0]}", "Content-Type": "application/json"}
                check_ledger(auth, negative == "ledger")
        except CheckFailed as e:
            results[name] = str(e)
            print(f"  FAILED: {e}")
        else:
            results[name] = ""
            print("  PASSED")

    print("\n== summary ==")
    for name in selected:
        print(f"  {'PASS' if not results[name] else 'FAIL'}  {name}")

    failed = [n for n in selected if results[n]]
    if failed:
        print(f"\nPOST-DEPLOY VERIFICATION FAILED: {', '.join(failed)}")
        for name in failed:
            print(f"  - {name}: {results[name]}")
        return 1
    # Name what actually ran. The old wording here claimed "chat answers,
    # sign-in completes, billing charges" for every green run including a
    # `--only chat` one, which is a pass claiming two checks it never made.
    claims = {"chat": "chat answers", "signin": "sign-in completes",
              "ledger": "billing charges"}
    print("\nPOST-DEPLOY VERIFICATION PASSED: "
          + ", ".join(claims[name] for name in selected))
    skipped = [claims[name] for name in CHECKS if name not in selected]
    if skipped:
        print(f"  NOT VERIFIED by this run: {', '.join(skipped)}")
    return 0


if __name__ == "__main__":
    sys.exit(main())

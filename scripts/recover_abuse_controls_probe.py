#!/usr/bin/env python3
"""Driver for scripts/test-recover-abuse-controls.sh (issue #1744).

Run through that script, which stands up the stack this talks to. Run on its
own it will fail to connect, which is the intended failure.

Delivered mail is the primary observable, not HTTP status, and deliberately so:
after the fix a refusal on /auth/v1/recover is indistinguishable from a send,
because a distinguishable one would tell an attacker which addresses hold
accounts. What is left to measure is whether the message actually went out, and
MailHog answers that.

"Indistinguishable" is compared as status, body AND the full response header
list, not status and body. The first version of this file compared only status
and body, and reported 14 green on a config whose two answers still differed in
`Vary`, `Server` and the `Via` hop count -- the same oracle at the same cost as
the status code it had just closed. A guard that cannot see the property it
guards is worse than no guard, because it also stops anyone else looking.
"""

from __future__ import annotations

import base64
import hashlib
import hmac
import json
import os
import sys
import time
import urllib.error
import urllib.request

CONSOLE_URL = os.environ.get("CONSOLE_URL", "http://127.0.0.1:8880")
GOTRUE_URL = os.environ.get("GOTRUE_URL", "http://127.0.0.1:8999")
MAILHOG_URL = os.environ.get("MAILHOG_URL", "http://127.0.0.1:8925")
JWT_SECRET = os.environ.get("JWT_SECRET", "")

# GoTrue hardcodes the burst of the limiter /recover is wrapped in to 30
# (newLimiterPer5mOver1h in internal/api/apilimiter/apilimiter.go), whatever
# GOTRUE_RATE_LIMIT_OTP is set to. So 31 requests is what exhausts one bucket.
BURST = 30

failures: list[str] = []


def check(ok: bool, what: str) -> None:
    print(("  PASS  " if ok else "  FAIL  ") + what)
    if not ok:
        failures.append(what)


def b64(raw: bytes) -> str:
    return base64.urlsafe_b64encode(raw).rstrip(b"=").decode()


def service_role_jwt() -> str:
    header = b64(json.dumps({"alg": "HS256", "typ": "JWT"}).encode())
    payload = b64(
        json.dumps(
            {
                "role": "service_role",
                "aud": "authenticated",
                "iss": "hive-1744-harness",
                "exp": int(time.time()) + 3600,
            }
        ).encode()
    )
    signing_input = f"{header}.{payload}".encode()
    sig = hmac.new(JWT_SECRET.encode(), signing_input, hashlib.sha256).digest()
    return f"{header}.{payload}.{b64(sig)}"


def post(url: str, body: dict, headers: dict) -> tuple[int, list[tuple[str, str]], str]:
    """Returns the response headers as a LIST, not a dict.

    A dict silently collapses repeated headers, and `Via` is repeated once per
    proxy hop: the difference between a proxied answer and a synthesized one is
    exactly one `Via`, so a dict here would hide half of what this file checks.
    """
    req = urllib.request.Request(
        url,
        data=json.dumps(body).encode(),
        headers={"Content-Type": "application/json", **headers},
        method="POST",
    )
    try:
        with urllib.request.urlopen(req, timeout=15) as resp:
            return resp.status, list(resp.headers.items()), resp.read().decode()
    except urllib.error.HTTPError as exc:
        return exc.code, list(exc.headers.items()), exc.read().decode()


def answer(response: tuple[int, list[tuple[str, str]], str]) -> tuple:
    """Everything about a response a caller can see, minus what legitimately
    varies per request. Date is the only such header here: Content-Length stays
    in, because two answers of different lengths are two distinguishable
    answers."""
    status, headers, body = response
    seen = sorted((k.lower(), v) for k, v in headers if k.lower() != "date")
    return (status, tuple(seen), body.strip())


def describe(response: tuple[int, list[tuple[str, str]], str]) -> str:
    status, headers, body = response
    names = ", ".join(sorted(k.lower() for k, _ in headers if k.lower() != "date"))
    return f"{status} {body.strip()!r} [{names}]"


def get_json(url: str) -> dict:
    with urllib.request.urlopen(url, timeout=15) as resp:
        return json.loads(resp.read().decode())


def recover_via_console(email: str, client_ip: str | None) -> tuple[int, list[tuple[str, str]], str]:
    """POST /auth/v1/recover the way a browser does, through both Caddies.

    client_ip is what Cloudflare would have reported. None omits the header,
    which is the pre-fix shape: nothing identifies the caller and every request
    keys on the proxy's own address.
    """
    headers = {"Host": "console.localhost"}
    if client_ip is not None:
        headers["CF-Connecting-IP"] = client_ip
    return post(f"{CONSOLE_URL}/auth/v1/recover", {"email": email}, headers)


def recover_direct(email: str, forwarded_for: str) -> tuple[int, list[tuple[str, str]], str]:
    """Same request against GoTrue itself, past the gateway.

    This is the only way to see what GoTrue actually answered, since the
    gateway rewrites its refusals on this route.
    """
    return post(f"{GOTRUE_URL}/recover", {"email": email}, {"X-Forwarded-For": forwarded_for})


def create_user(email: str) -> None:
    status, _, body = post(
        f"{GOTRUE_URL}/admin/users",
        {"email": email, "password": "harness-password-1744", "email_confirm": True},
        {"Authorization": "Bearer " + service_role_jwt()},
    )
    if status not in (200, 201):
        print(f"could not create {email}: {status} {body}", file=sys.stderr)
        sys.exit(2)


def mail_count(email: str) -> int:
    data = get_json(f"{MAILHOG_URL}/api/v2/messages?limit=500")
    n = 0
    for item in data.get("items", []):
        for to in item.get("To", []):
            if f"{to.get('Mailbox')}@{to.get('Domain')}" == email:
                n += 1
    return n


def settle() -> None:
    """MailHog records a message a moment after GoTrue's response returns."""
    time.sleep(1.0)


def main() -> int:
    users = [f"u{i}@example.test" for i in range(1, 7)]
    for email in users:
        create_user(email)

    print("\n1. the 200-either-way property, which both changes have to preserve")
    unknown = recover_via_console("nobody-at-all@example.test", "203.0.113.1")
    known_before = answer(unknown)
    check(
        unknown[0] == 200 and unknown[2].strip() == "{}",
        f"unknown address answers 200 {{}} (got {describe(unknown)})",
    )

    real = recover_via_console(users[0], "203.0.113.2")
    check(
        answer(real) == known_before,
        f"a real address answers identically, headers included (got {describe(real)})",
    )
    settle()
    check(mail_count(users[0]) == 1, "and the real address was actually mailed")
    # The gateway restates GoTrue's CORS headers on this route because it
    # synthesizes the response rather than proxying it. Two of them would be
    # worse than none: a browser rejects a duplicated
    # Access-Control-Allow-Origin exactly as hard as a missing one, and the
    # structural check in test_caddy_supabase_routes.py can only read text.
    acao = [v for k, v in unknown[1] if k.lower() == "access-control-allow-origin"]
    check(acao == ["*"], f"exactly one Access-Control-Allow-Origin reaches the caller (got {acao})")

    print("\n2. the per-address cap holds across source addresses")
    for ip in ("203.0.113.3", "203.0.113.4", "203.0.113.5"):
        throttled = recover_via_console(users[0], ip)
        check(
            answer(throttled) == known_before,
            f"a throttled address answers identically from {ip} (got {describe(throttled)})",
        )
    settle()
    check(
        mail_count(users[0]) == 1,
        f"three more requests from three more addresses delivered no more mail (count={mail_count(users[0])})",
    )

    print("\n3. one caller exhausting its quota does not deny another")
    noisy, quiet = "198.51.100.10", "198.51.100.20"
    for _ in range(BURST + 1):
        recover_via_console(users[1], noisy)
    served = recover_via_console(users[2], quiet)
    check(
        answer(served) == known_before,
        f"a second caller is still served after the first burned {BURST + 1} requests "
        f"(got {describe(served)})",
    )
    settle()
    check(mail_count(users[2]) == 1, "and that second caller's mail was actually sent")

    print("\n4. the first caller really was refused, and the gateway really hides it")
    raw = recover_direct(users[3], noisy)
    check(raw[0] == 429, f"GoTrue itself refuses the exhausted caller (got {raw[0]})")
    check(
        any(k.lower() == "x-sb-error-code" for k, _ in raw[1]),
        "and names the reason in a header, which is why the rewrite cannot copy "
        "upstream headers through",
    )
    masked = recover_via_console(users[3], noisy)
    check(
        not any(k.lower() == "x-sb-error-code" for k, _ in masked[1]),
        "and that header does not reach the browser",
    )
    check(
        answer(masked) == known_before,
        f"the same refusal reaches the browser byte for byte as the unknown-address "
        f"answer (got {describe(masked)})",
    )
    settle()
    check(mail_count(users[3]) == 0, "and no mail was sent, so the 200 is not a send")

    print("\n5. the client address is what splits the buckets, not something else")
    # The pre-fix shape: no client address on the request at all, so every
    # caller keys on the gateway's own peer address and shares one bucket. If
    # this does not deny the last request, the harness cannot see the defect it
    # was written for and nothing above means anything.
    for _ in range(BURST + 1):
        recover_via_console(users[4], None)
    settle()
    before = mail_count(users[5])
    recover_via_console(users[5], None)
    settle()
    check(
        mail_count(users[5]) == before,
        "sharing one bucket denies the next caller, which is the defect (control)",
    )
    recover_via_console(users[5], "198.51.100.30")
    settle()
    check(
        mail_count(users[5]) == before + 1,
        "and naming the client address serves that same caller (fix)",
    )

    print()
    if failures:
        print(f"recover abuse controls: FAIL ({len(failures)} of the checks above)")
        return 1
    print("recover abuse controls: OK")
    return 0


if __name__ == "__main__":
    sys.exit(main())

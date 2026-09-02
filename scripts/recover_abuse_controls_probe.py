#!/usr/bin/env python3
"""Driver for scripts/test-recover-abuse-controls.sh (issue #1744).

Run through that script, which stands up the stack this talks to. Run on its
own it will fail to connect, which is the intended failure.

Delivered mail is the primary observable, not HTTP status, and deliberately so:
after the fix a refusal on /auth/v1/recover is indistinguishable from a send,
because a distinguishable one would tell an attacker which addresses hold
accounts. What is left to measure is whether the message actually went out, and
MailHog answers that.
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


def post(url: str, body: dict, headers: dict) -> tuple[int, str]:
    req = urllib.request.Request(
        url,
        data=json.dumps(body).encode(),
        headers={"Content-Type": "application/json", **headers},
        method="POST",
    )
    try:
        with urllib.request.urlopen(req, timeout=15) as resp:
            return resp.status, resp.read().decode()
    except urllib.error.HTTPError as exc:
        return exc.code, exc.read().decode()


def get_json(url: str) -> dict:
    with urllib.request.urlopen(url, timeout=15) as resp:
        return json.loads(resp.read().decode())


def recover_via_console(email: str, client_ip: str | None) -> tuple[int, str]:
    """POST /auth/v1/recover the way a browser does, through both Caddies.

    client_ip is what Cloudflare would have reported. None omits the header,
    which is the pre-fix shape: nothing identifies the caller and every request
    keys on the proxy's own address.
    """
    headers = {"Host": "console.localhost"}
    if client_ip is not None:
        headers["CF-Connecting-IP"] = client_ip
    return post(f"{CONSOLE_URL}/auth/v1/recover", {"email": email}, headers)


def recover_direct(email: str, forwarded_for: str) -> tuple[int, str]:
    """Same request against GoTrue itself, past the gateway.

    This is the only way to see what GoTrue actually answered, since the
    gateway rewrites its refusals on this route.
    """
    return post(f"{GOTRUE_URL}/recover", {"email": email}, {"X-Forwarded-For": forwarded_for})


def create_user(email: str) -> None:
    status, body = post(
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
    status, body = recover_via_console("nobody-at-all@example.test", "203.0.113.1")
    known_before = (status, body.strip())
    check(status == 200 and body.strip() == "{}", f"unknown address answers 200 {{}} (got {status} {body.strip()!r})")

    status, body = recover_via_console(users[0], "203.0.113.2")
    check(
        (status, body.strip()) == known_before,
        f"a real address answers identically (got {status} {body.strip()!r})",
    )
    settle()
    check(mail_count(users[0]) == 1, "and the real address was actually mailed")

    print("\n2. the per-address cap holds across source addresses")
    for ip in ("203.0.113.3", "203.0.113.4", "203.0.113.5"):
        status, body = recover_via_console(users[0], ip)
        check(
            (status, body.strip()) == known_before,
            f"a throttled address answers identically from {ip} (got {status} {body.strip()!r})",
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
    status, body = recover_via_console(users[2], quiet)
    check(
        (status, body.strip()) == known_before,
        f"a second caller is still served after the first burned {BURST + 1} requests (got {status})",
    )
    settle()
    check(mail_count(users[2]) == 1, "and that second caller's mail was actually sent")

    print("\n4. the first caller really was refused, and the gateway really hides it")
    status, _ = recover_direct(users[3], noisy)
    check(status == 429, f"GoTrue itself refuses the exhausted caller (got {status})")
    status, body = recover_via_console(users[3], noisy)
    check(
        (status, body.strip()) == known_before,
        f"the same refusal reaches the browser as 200 {{}} (got {status} {body.strip()!r})",
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

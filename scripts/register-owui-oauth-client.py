#!/usr/bin/env python3
"""Register the Open WebUI OAuth client on a self-hosted GoTrue.

Why this exists
---------------
Open WebUI signs in through Supabase's OAuth 2.1 authorization server
(OPENID_PROVIDER_URL in deploy/docker/docker-compose.yml), using a client id
and secret. On hosted Supabase that client is dashboard state: nothing in this
repository creates it, nothing tracks it, and it leaves no diff. Delete the
hosted project and it is simply gone, with no error to read, and chat login
stops working for a reason nothing in the tree explains.

Self-hosting is the chance to fix that. GoTrue exposes the same registration
over its admin API, so the client becomes a bring-up step a fresh environment
can run for itself.

Requires GoTrue v2.180.0 or newer. The old enterprise pin (v2.170.0) has no
OAuth server at all, and answers 404 on every route this script uses.

Usage
-----
GoTrue is not published on a host port, so this runs on the compose network
rather than from a host shell, where "caddy-supabase" does not resolve. Pass
the service_role key by FILE, never as a literal on the command line: a value
in argv is visible in ps for the container's life, in shell history, and in
docker inspect afterwards.

    docker run --rm --network <project>_default
        --env-file /path/to/.env
        -e GOTRUE_ADMIN_URL=http://caddy-supabase/auth/v1
        -e OWUI_REDIRECT_URI=https://chat.example.com/oauth/oidc/callback
        -e HIVE_CHAT_URL=https://chat.example.com
        -v "$PWD/scripts:/s:ro" python:3.12-alpine python3 /s/register-owui-oauth-client.py

The env file needs SERVICE_ROLE_KEY; the enterprise .env already carries the
same value as ENTERPRISE_SERVICE_ROLE_KEY, so either add the alias or export it
from a wrapper. The network name is the compose project name plus "_default";
a stack started as "-p hive" gives "hive_default".

Keep GOTRUE_ADMIN_URL on the compose network. The service_role key travels in
an Authorization header, so a plain http URL that leaves the box puts an
omnipotent credential on the wire, and the public listener refuses
/auth/v1/admin/* by design anyway.

Prints the two .env lines on success. The secret is shown exactly once, at
creation, because GoTrue stores only its hash. Idempotent: a client already
registered with the same name and the same redirect URIs is reported and left
alone, since re-registering would mint a second client and a second secret.

Add --self-check to run the offline guards instead of talking to anything.
"""

import argparse
import json
import os
import sys
import urllib.error
import urllib.request

# GoTrue matches redirect_uris by exact string (isValidRedirectURI upstream),
# so a trailing slash, a different scheme, or a different host is a different
# URI and the authorize call fails with invalid redirect_uri. Open WebUI posts
# back to /oauth/oidc/callback on its own origin.
CALLBACK_PATH = "/oauth/oidc/callback"

CLIENT_NAME = "Hive Chat"


def build_payload(redirect_uris, client_uri, client_name=CLIENT_NAME):
    """The registration body. Confidential client, authorization code with
    refresh, secret sent with HTTP Basic, which is what Open WebUI does."""
    return {
        "client_name": client_name,
        "redirect_uris": list(redirect_uris),
        "grant_types": ["authorization_code", "refresh_token"],
        "response_types": ["code"],
        "token_endpoint_auth_method": "client_secret_basic",
        "client_uri": client_uri,
    }


def find_existing(clients, payload):
    """Return the already-registered client matching this registration, or None.

    Matched on name AND the exact redirect URI set, so a client registered for
    a different environment's callback is correctly treated as a different
    client rather than silently reused with the wrong redirect.
    """
    want = sorted(payload["redirect_uris"])
    for c in clients:
        if c.get("client_name") != payload["client_name"]:
            continue
        if sorted(c.get("redirect_uris") or []) == want:
            return c
    return None


def api(base, path, token, method="GET", body=None):
    req = urllib.request.Request(
        base.rstrip("/") + path,
        method=method,
        data=json.dumps(body).encode() if body is not None else None,
        headers={
            "Authorization": "Bearer " + token,
            "Content-Type": "application/json",
            "Accept": "application/json",
        },
    )
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            raw = resp.read().decode("utf-8", "replace")
            return resp.status, (json.loads(raw) if raw else {})
    except urllib.error.HTTPError as exc:
        raw = exc.read().decode("utf-8", "replace")
        if exc.code == 404:
            raise SystemExit(
                "GoTrue answered 404 for " + path + ". Most likely this is pointed at "
                "the PUBLIC gateway listener, which refuses /auth/v1/admin/* on purpose: "
                "use the in-network address. Otherwise the OAuth server is off "
                "(GOTRUE_OAUTH_SERVER_ENABLED) or the image predates v2.180.0."
            ) from None
        if exc.code in (401, 403):
            raise SystemExit(
                "GoTrue rejected the credential (HTTP " + str(exc.code) + "). This "
                "needs the service_role key, not the anon key."
            ) from None
        raise SystemExit("GoTrue error HTTP " + str(exc.code) + ": " + raw[:300]) from None
    except urllib.error.URLError as exc:
        raise SystemExit("could not reach GoTrue at " + base + ": " + str(exc.reason)) from None


def self_check():
    """Offline guards. No network, no framework."""
    payload = build_payload(["https://chat.example.com" + CALLBACK_PATH], "https://chat.example.com")
    assert payload["token_endpoint_auth_method"] == "client_secret_basic"
    assert payload["response_types"] == ["code"]
    assert "refresh_token" in payload["grant_types"], "OWUI needs refresh to keep a session"
    assert payload["redirect_uris"] == ["https://chat.example.com/oauth/oidc/callback"]

    # Exact-string matching is the whole risk here, so the guards are about
    # near-misses, not about the happy path.
    assert find_existing([], payload) is None
    same = {"client_name": CLIENT_NAME, "redirect_uris": list(payload["redirect_uris"]), "client_id": "abc"}
    assert find_existing([same], payload) is same, "an identical registration must be reused"

    trailing = {"client_name": CLIENT_NAME, "redirect_uris": ["https://chat.example.com/oauth/oidc/callback/"]}
    assert find_existing([trailing], payload) is None, "a trailing slash is a different URI"

    other_host = {"client_name": CLIENT_NAME, "redirect_uris": ["https://other.example.com" + CALLBACK_PATH]}
    assert find_existing([other_host], payload) is None, "another host is a different client"

    other_name = {"client_name": "Something Else", "redirect_uris": list(payload["redirect_uris"])}
    assert find_existing([other_name], payload) is None

    # Order must not matter when a client carries several callbacks.
    multi = build_payload(["https://b.example/x", "https://a.example/y"], "https://a.example")
    reversed_order = {"client_name": CLIENT_NAME, "redirect_uris": ["https://a.example/y", "https://b.example/x"]}
    assert find_existing([reversed_order], multi) is reversed_order

    print("register-owui-oauth-client self-check: OK")
    return 0


def main():
    parser = argparse.ArgumentParser(description="Register the Open WebUI OAuth client")
    parser.add_argument("--self-check", action="store_true", help="run offline guards and exit")
    args = parser.parse_args()
    if args.self_check:
        return self_check()

    base = os.environ.get("GOTRUE_ADMIN_URL", "http://caddy-supabase/auth/v1").strip()
    token = os.environ.get("SERVICE_ROLE_KEY", "").strip()
    chat_url = os.environ.get("HIVE_CHAT_URL", "").strip().rstrip("/")
    redirect = os.environ.get("OWUI_REDIRECT_URI", "").strip()

    if not token:
        print("SERVICE_ROLE_KEY is required (the service_role JWT, not the anon key)", file=sys.stderr)
        return 2
    if not redirect:
        if not chat_url:
            print(
                "Set OWUI_REDIRECT_URI to Open WebUI's callback, or HIVE_CHAT_URL to its "
                "origin. GoTrue matches this by exact string, so it must be byte identical "
                "to what Open WebUI sends, trailing slash included.",
                file=sys.stderr,
            )
            return 2
        redirect = chat_url + CALLBACK_PATH
    client_uri = chat_url or redirect.split(CALLBACK_PATH)[0]

    payload = build_payload([redirect], client_uri)

    # Insurance, not a live fix: at v2.189.0 this handler carries an explicit
    # "TODO(cemal) :: Add pagination" and returns every row unbounded, so
    # per_page is ignored and the response is always complete. The refusal
    # below therefore cannot fire on this pin. It stays because the day
    # upstream does paginate, reading only the first page would silently
    # register a SECOND client with a second secret, which is exactly what
    # this check exists to prevent.
    # ponytail: one large page plus a refusal, rather than a pagination loop.
    # This deployment registers one client; the loop is the upgrade if that
    # ever stops being true, and until then refusing beats guessing.
    page_size = 1000
    _, listing = api(base, "/admin/oauth/clients?per_page=" + str(page_size), token)
    clients = listing.get("clients", listing) if isinstance(listing, dict) else listing
    clients = clients or []
    if len(clients) >= page_size:
        raise SystemExit(
            "GoTrue returned a full page of OAuth clients, so the listing may be "
            "truncated and an existing registration could be missed. Refusing to "
            "register rather than risk a duplicate client; check by hand."
        )
    existing = find_existing(clients, payload)
    if existing:
        print("# Client already registered; leaving it alone. GoTrue stores only a hash")
        print("# of the secret, so the secret cannot be printed again. Rotate through")
        print("# GoTrue if it has been lost.")
        print("SUPABASE_OAUTH_CLIENT_ID=" + str(existing.get("client_id", "")))
        return 0

    status, created = api(base, "/admin/oauth/clients", token, method="POST", body=payload)
    if status not in (200, 201) or not created.get("client_id"):
        print("unexpected registration response: HTTP " + str(status), file=sys.stderr)
        return 1

    print("# Paste into .env. The secret is shown once and never again.")
    print("SUPABASE_OAUTH_CLIENT_ID=" + created["client_id"])
    print("SUPABASE_OAUTH_CLIENT_SECRET=" + created.get("client_secret", ""))
    print("# redirect_uri registered: " + redirect, file=sys.stderr)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

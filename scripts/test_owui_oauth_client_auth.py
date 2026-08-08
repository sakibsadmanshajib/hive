#!/usr/bin/env python3
"""Self-check for the Open WebUI OAuth refresh client-auth patch (issue #782).

Open WebUI exchanges the authorization code through authlib, which defaults
`token_endpoint_auth_method` to `client_secret_basic` whenever a client secret
is set, but it hand builds the refresh POST with `client_id` and
`client_secret` in the form body, which is `client_secret_post`. Providers that
enforce the registered method, Supabase among them, accept the login and then
answer every refresh with 400 invalid_credentials. Open WebUI reads that as a
dead session, deletes it, and the user cannot chat again until they sign in
through SSO, roughly 55 minutes after they did so last.

deploy/docker/owui-patches/hive_oauth_client_auth.py makes the refresh leg
authenticate the way the exchange leg already does. This file exercises it
directly: no framework, no network, no Open WebUI import.
Run: python3 scripts/test_owui_oauth_client_auth.py
"""
import base64
import importlib.util
import sys
from pathlib import Path

MODULE_PATH = (
    Path(__file__).resolve().parents[1]
    / "deploy"
    / "docker"
    / "owui-patches"
    / "hive_oauth_client_auth.py"
)
spec = importlib.util.spec_from_file_location("hive_oauth_client_auth", MODULE_PATH)
hive_oauth_client_auth = importlib.util.module_from_spec(spec)
spec.loader.exec_module(hive_oauth_client_auth)

hive_refresh_request = hive_oauth_client_auth.hive_refresh_request
resolve_auth_method = hive_oauth_client_auth.resolve_auth_method

CLIENT_ID = "hive-owui-client"
CLIENT_SECRET = "s3cr3t-value"


class FakeClient:
    """Stand-in for the authlib StarletteOAuth2App Open WebUI keeps per
    provider. Only the three attributes the helper reads are modelled."""

    def __init__(self, client_secret=CLIENT_SECRET, auth_method=None, client_id=CLIENT_ID):
        self.client_id = client_id
        self.client_secret = client_secret
        self.client_kwargs = {"scope": "openid email profile offline_access"}
        if auth_method is not None:
            self.client_kwargs["token_endpoint_auth_method"] = auth_method


def upstream_body(client=None):
    """The body upstream's _perform_token_refresh builds, verbatim."""
    client = client or FakeClient()
    body = {
        "grant_type": "refresh_token",
        "refresh_token": "rt-abc123",
        "client_id": client.client_id,
    }
    if client.client_secret:
        body["client_secret"] = client.client_secret
    return body


def decode_basic(header):
    assert header.startswith("Basic "), header
    return base64.b64decode(header[len("Basic ") :]).decode("utf-8")


def test_unset_method_resolves_to_basic_like_authlib():
    # authlib: `if token_endpoint_auth_method is None: client_secret_basic`
    # when a secret exists, else "none". The refresh leg has to agree, because
    # the exchange leg is what the provider registered against.
    assert resolve_auth_method(FakeClient()) == "client_secret_basic"
    assert resolve_auth_method(FakeClient(client_secret=None)) == "none"
    assert resolve_auth_method(FakeClient(auth_method="client_secret_post")) == "client_secret_post"


def test_basic_moves_credentials_out_of_the_body():
    client = FakeClient()
    sent = hive_refresh_request(client, upstream_body(client))

    # The whole defect: these two keys in the body are what made Supabase call
    # the request client_secret_post and refuse it.
    assert "client_secret" not in sent["data"], sent["data"]
    assert "client_id" not in sent["data"], sent["data"]

    # The grant itself must survive untouched.
    assert sent["data"]["grant_type"] == "refresh_token"
    assert sent["data"]["refresh_token"] == "rt-abc123"

    assert decode_basic(sent["headers"]["Authorization"]) == f"{CLIENT_ID}:{CLIENT_SECRET}"
    assert sent["headers"]["Content-Type"] == "application/x-www-form-urlencoded"


def test_post_registration_keeps_upstream_behaviour():
    # A deployment whose provider really is registered client_secret_post must
    # keep working exactly as before, so this patch cannot break anyone who
    # was not already broken.
    client = FakeClient(auth_method="client_secret_post")
    sent = hive_refresh_request(client, upstream_body(client))

    assert sent["data"]["client_id"] == CLIENT_ID
    assert sent["data"]["client_secret"] == CLIENT_SECRET
    assert "Authorization" not in sent["headers"], sent["headers"]


def test_public_client_sends_no_credentials_anywhere():
    # No secret means nothing to put in a header. PKCE carries the proof.
    client = FakeClient(client_secret=None)
    sent = hive_refresh_request(client, upstream_body(client))

    assert "Authorization" not in sent["headers"], sent["headers"]
    assert sent["data"]["client_id"] == CLIENT_ID
    assert "client_secret" not in sent["data"], sent["data"]


def test_basic_credential_is_form_urlencoded_per_rfc6749():
    # RFC 6749 section 2.3.1 encodes each half before the base64. A secret
    # containing a colon would otherwise split into the wrong two fields at
    # the provider and read as a wrong-password failure.
    client = FakeClient(client_secret="pa:ss word+plus")
    sent = hive_refresh_request(client, upstream_body(client))

    assert decode_basic(sent["headers"]["Authorization"]) == f"{CLIENT_ID}:pa%3Ass%20word%2Bplus"


def test_caller_body_is_not_mutated():
    # The helper is called inside the POST expression; upstream still holds a
    # reference to the dict it built and logs it on failure.
    client = FakeClient()
    body = upstream_body(client)
    hive_refresh_request(client, body)

    assert body["client_id"] == CLIENT_ID
    assert body["client_secret"] == CLIENT_SECRET


def main() -> None:
    for name, fn in sorted(globals().items()):
        if name.startswith("test_") and callable(fn):
            fn()
    print("ok: owui OAuth refresh client auth (issue #782)")


if __name__ == "__main__":
    sys.exit(main())

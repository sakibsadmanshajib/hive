#!/usr/bin/env python3
"""Scrub credential-bearing values out of a log stream on its way to a file.

Reads stdin, writes stdout. Used by the workflow steps that dump container
logs into an artifact, because this repository is public and every artifact is
downloadable by anyone holding the run URL. Open WebUI's uvicorn access lines
carry the OAuth callback verbatim, so an unfiltered dump publishes
`GET /oauth/oidc/callback?code=...` for a real account.

The parameter list and the bare-JWT pattern mirror `redactSecrets` in
apps/web-console/tests/e2e/support/e2e-fixture-seed.mjs. Keep them in step.

CREDENTIALS AFTER A FRAGMENT COUNT. A redactor that only understands query
strings is not a redactor: GoTrue hands a session back as a redirect whose
credentials live after the "#", and an agent's own query-string-only scrubber
printed a live session token to stdout on 2026-08-08 while looking like it was
protecting the log. The pattern below matches `name=value` wherever it appears.

Run: docker compose logs ... | python3 scripts/redact-log-credentials.py > out
Self-check: python3 scripts/redact-log-credentials.py --selfcheck
"""
import re
import sys

CREDENTIAL_PARAMS = [
    "access_token",
    "refresh_token",
    "provider_token",
    "provider_refresh_token",
    "id_token",
    "token_hash",
    "hashed_token",
    "confirmation_token",
    "recovery_token",
    "invitation_token",
    "invite_token",
    "email_otp",
    "client_secret",
    "api_key",
    "apikey",
    "password",
    "secret",
    "token",
    "code",
    "otp",
]

# Longest names first so `provider_refresh_token` never degrades to `token`.
#
# The optional leading `[A-Za-z0-9_]*` matters more than it looks. `\b` treats
# `_` as a word character, so a bare `\bpassword=` does NOT match
# `RAG_VERIFY_PASSWORD=`, and environment-variable spelling is the dominant
# shape of a credential in a container log dump, which is precisely what this
# script is pointed at. Without the prefix the redactor would miss the case it
# meets most often.
CREDENTIAL_PARAM_RE = re.compile(
    r"\b([A-Za-z0-9_]*(?:"
    + "|".join(sorted(CREDENTIAL_PARAMS, key=len, reverse=True))
    + r"))=([^&#\s\"'\\]+)",
    re.IGNORECASE,
)

# A bare JWT with no parameter name around it: a session token on its own line,
# an `Authorization: Bearer ...` header, or a service-role key.
BARE_JWT_RE = re.compile(r"\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}")

# `hk_` is this project's API key prefix (see random_api_key in
# scripts/seed-owui-e2e-user.py), and a shim key reaches the logs on every
# edge-api request that carries one.
HIVE_API_KEY_RE = re.compile(r"\bhk_[A-Za-z0-9_-]{8,}")


def redact(text: str) -> str:
    text = CREDENTIAL_PARAM_RE.sub(lambda m: f"{m.group(1)}=<redacted>", text)
    text = BARE_JWT_RE.sub("<redacted>", text)
    return HIVE_API_KEY_RE.sub("<redacted>", text)


def selfcheck() -> None:
    # The whole point: a fragment-borne session token, which is the exact shape
    # that leaked on 2026-08-08.
    out = redact("redirect to https://app.example/#access_token=abc123&refresh_token=def456")
    assert "abc123" not in out and "def456" not in out, out

    # The OAuth callback an OWUI access line prints verbatim.
    out = redact('INFO: GET /oauth/oidc/callback?code=4%2F0Ab_live&state=xyz HTTP/1.1" 302')
    assert "4%2F0Ab_live" not in out, out
    assert "state=xyz" in out, "only credential parameters are scrubbed"

    # Longest-name-first: the specific name survives, the value does not.
    out = redact("provider_refresh_token=zzz")
    assert out == "provider_refresh_token=<redacted>", out

    # Environment-variable spelling. `\b` treats `_` as a word character, so a
    # bare `\bpassword=` misses these, and they are the common shape in a
    # container log dump and in what verify-rag-roundtrip.py prints.
    for line in (
        "export RAG_VERIFY_PASSWORD=hunter2live",
        "OWUI_E2E_PASSWORD=hunter2live",
        "E2E_VERIFIED_PASSWORD=hunter2live",
    ):
        out = redact(line)
        assert "hunter2live" not in out, out
        assert "PASSWORD=<redacted>" in out, out

    # Bare tokens with no parameter name around them.
    jwt = "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTYifQ.dBjftJeZ4CVPmB92K27uhbUJU1p1r"
    assert jwt not in redact(f"Authorization: Bearer {jwt}")
    assert "hk_livekeyvalue123" not in redact("used key hk_livekeyvalue123 for request")

    # Ordinary log text is untouched, so a scrubbed dump is still readable.
    plain = "control-plane  | 2026-08-11T00:00:00Z INFO served 200 in 12ms"
    assert redact(plain) == plain

    print("ok: redact-log-credentials scrubs fragments, query strings, bare tokens")


def main() -> None:
    if "--selfcheck" in sys.argv[1:]:
        selfcheck()
        return
    for line in sys.stdin:
        sys.stdout.write(redact(line))


if __name__ == "__main__":
    main()

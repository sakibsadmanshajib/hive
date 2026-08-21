#!/usr/bin/env python3
"""Refuse an env file that points half of the stack at a hosted Supabase project
and the other half at the self-hosted data plane.

Why this exists
---------------
The self-hosted values in docker-compose.enterprise.yml are `${VAR:-default}`
forms, so a variable that is set and non-empty beats them. A cutover that
rewrites SUPABASE_URL and SUPABASE_DB_URL but leaves a stale hosted
SUPABASE_JWKS_URL behind therefore comes up healthy, serves traffic, and quietly
keeps a Supabase Cloud project as a valid token issuer for this deployment. Every
consumer is happy, nothing logs anything, and the mixed state survives until the
cloud project is deleted underneath it.

This is the check for that, and it deliberately reads a real env file rather than
anything in the repository, because the mixed state only ever exists in one.

Usage
-----
    check-env-supabase-target.py .env
    check-env-supabase-target.py --self-check

Exit 0 when the file is coherently hosted OR coherently self-hosted, 1 when it
mixes the two or is internally inconsistent. Values are never printed: the report
names variables and a verdict.
"""
from __future__ import annotations

import argparse
import re
import sys
from pathlib import Path

# A hosted Supabase project, in any of the forms these variables can carry.
HOSTED = re.compile(r"\.supabase\.co\b|\.pooler\.supabase\.com\b", re.IGNORECASE)

# Every variable that decides where a server-side consumer looks for Supabase.
# NEXT_PUBLIC_* and SUPABASE_PUBLIC_URL are deliberately absent: they are
# browser-facing and a self-hosted deployment reaches them through a public
# origin, which is a different address from the in-network gateway and is not
# evidence of a mixed state.
SERVER_SIDE = (
    "SUPABASE_URL",
    "SUPABASE_DB_URL",
    "SUPABASE_DB_POOL_URL",
    "SUPABASE_DB_POOL_URL_LIBPQ",
    "SUPABASE_JWT_ISSUER",
    "SUPABASE_JWKS_URL",
    "S3_ENDPOINT",
)

# What docker-compose.enterprise.yml defaults the auth origin to when neither
# ENTERPRISE_AUTH_EXTERNAL_URL nor SUPABASE_JWT_ISSUER is set. Kept in step with
# that file by scripts/test_selfhost_supabase_seam.py, which asserts both sides
# of the compose default derive from the same variable.
COMPOSE_DEFAULT_AUTH_ORIGIN = "http://caddy-supabase/auth/v1"

# Set only by a deployment running its own data plane.
SELF_HOSTED_MARKERS = ("ENTERPRISE_DB_PASSWORD", "ENTERPRISE_JWT_SECRET")


def parse_env(text: str) -> dict:
    out = {}
    for line in text.splitlines():
        stripped = line.strip()
        if not stripped or stripped.startswith("#") or "=" not in stripped:
            continue
        key, _, value = stripped.partition("=")
        key = key.strip()
        if key.startswith("export "):
            key = key[len("export ") :].strip()
        if not re.fullmatch(r"[A-Za-z_][A-Za-z0-9_]*", key):
            continue
        value = value.strip()
        if len(value) >= 2 and value[0] == value[-1] and value[0] in "\"'":
            value = value[1:-1]
        out[key] = value
    return out


def check(env: dict) -> tuple[int, list]:
    """Return (exit code, report lines). Never includes a value."""
    report = []
    self_hosted = [k for k in SELF_HOSTED_MARKERS if env.get(k)]
    hosted_vars = [k for k in SERVER_SIDE if env.get(k) and HOSTED.search(env[k])]

    if not self_hosted:
        report.append("no ENTERPRISE_* data-plane secret set, treating this as a hosted deployment")
        if hosted_vars:
            report.append(f"hosted targets, consistent with that: {' '.join(hosted_vars)}")
        return 0, report

    report.append(f"self-hosted data plane configured ({' '.join(self_hosted)})")
    failed = False

    if hosted_vars:
        report.append(
            "MIXED STATE: these still point at a hosted Supabase project and will "
            f"beat the enterprise defaults: {' '.join(hosted_vars)}"
        )
        failed = True

    jwks = env.get("SUPABASE_JWKS_URL", "")
    if jwks and not jwks.startswith("https://"):
        report.append("SUPABASE_JWKS_URL is not https, and edge-api refuses a plain-http JWKS URL")
        failed = True
    if jwks and not env.get("SUPABASE_JWKS_CA_FILE"):
        report.append(
            "SUPABASE_JWKS_URL is set but SUPABASE_JWKS_CA_FILE is not, so the "
            "gateway's local certificate authority is not trusted and the first "
            "JWKS refresh fails at boot"
        )
        failed = True

    # Both sides fall back to what docker-compose.enterprise.yml defaults them
    # to, because an unset variable is not an absent value: compose hands GoTrue
    # that default and edge-api compares against the same expression. Requiring
    # BOTH to be set before comparing meant an env file that pinned
    # SUPABASE_JWT_ISSUER to something else and left
    # ENTERPRISE_AUTH_EXTERNAL_URL alone sailed through, which is the exact
    # mismatch this check exists for: GoTrue stamps the default, edge-api expects
    # the pin, and every token is rejected with nothing logged.
    external = env.get("ENTERPRISE_AUTH_EXTERNAL_URL", "") or COMPOSE_DEFAULT_AUTH_ORIGIN
    issuer = env.get("SUPABASE_JWT_ISSUER", "") or external
    # EXACT comparison, not one with the slashes stripped off both sides.
    # Nothing downstream is lenient: compose passes these strings through
    # verbatim, and edge-api hands the issuer to jwt.WithIssuer, which is a
    # string equality test. So a trailing slash on one side only is a real
    # mismatch that rejects every token GoTrue issues, and stripping it here
    # reported the environment as fine while the box was broken. Third instance
    # of this defect on one branch, after two on the gateway path matchers, which
    # is why the rule below refuses the character outright rather than tolerating
    # it at a fourth site.
    if issuer != external:
        report.append(
            "SUPABASE_JWT_ISSUER and ENTERPRISE_AUTH_EXTERNAL_URL disagree, so "
            "GoTrue stamps an issuer edge-api rejects and every token fails with "
            "no boot error anywhere"
        )
        failed = True

    # No trailing slash on any URL variable, which is the general form of the
    # defect above rather than one more instance of it.
    #
    # Two distinct things go wrong with a trailing slash, and tolerating it at
    # the point of comparison only ever fixes the first:
    #
    #   * exact comparison. The issuer check above, and edge-api's own
    #     jwt.WithIssuer, are string equality.
    #   * string concatenation. docker-compose.yml builds Open WebUI's discovery
    #     URL from a base plus /auth/v1/.well-known/openid-configuration, so a
    #     base ending in a slash yields a double slash in the path and the
    #     discovery fetch 404s. A comparison that stripped slashes would have
    #     called that environment healthy.
    #
    # Refusing the character is one rule that covers both, and it costs an
    # operator nothing: none of these values is meaningful with a trailing slash.
    for key in (
        "SUPABASE_URL",
        "SUPABASE_PUBLIC_URL",
        "SUPABASE_JWT_ISSUER",
        "SUPABASE_JWKS_URL",
        "ENTERPRISE_AUTH_EXTERNAL_URL",
        "S3_ENDPOINT",
        "NEXT_PUBLIC_SUPABASE_URL",
        # GoTrue concatenates GOTRUE_SITE_URL with
        # GOTRUE_OAUTH_SERVER_AUTHORIZATION_PATH to build the consent redirect,
        # so a trailing slash here sends the browser to //oauth/consent.
        "ENTERPRISE_SITE_URL",
    ):
        if env.get(key, "").endswith("/"):
            report.append(
                f"{key} ends with a trailing slash. These values are compared "
                "exactly and concatenated with a path, so a trailing slash "
                "either rejects every token or produces a double slash the "
                "upstream answers 404 to"
            )
            failed = True

    # The browser-facing half of the cutover, which the checks above are blind
    # to by design: they deliberately ignore SUPABASE_PUBLIC_URL and
    # NEXT_PUBLIC_* because a public origin is not evidence of a mixed state.
    # That leaves the failure mode this block exists for, and it is not a mixed
    # state, it is a HALF-MOVED one. Four values describe the same browser-facing
    # auth origin and each is read by a different consumer:
    #
    #   SUPABASE_PUBLIC_URL          Open WebUI's OIDC discovery URL is built
    #                                from it (docker-compose.yml).
    #   NEXT_PUBLIC_SUPABASE_URL     inlined into the web-console bundle by
    #                                `next build`, so it needs a rebuild to move.
    #   ENTERPRISE_AUTH_EXTERNAL_URL GoTrue's API_EXTERNAL_URL and its `iss`
    #                                claim, so it decides what the discovery
    #                                document advertises.
    #   ENTERPRISE_SITE_URL          GoTrue's SITE_URL: where the OAuth consent
    #                                page and every mail link resolve.
    #
    # Move a subset and every service still starts, every health check still
    # passes, and the only symptom is a browser that cannot log in, reported by
    # a user rather than by anything here. On this deployment the live state was
    # exactly that: three of the four still named either a compose service name
    # or the hosted project, and the fourth named the operator's own laptop.
    public = env.get("SUPABASE_PUBLIC_URL", "")
    next_public = env.get("NEXT_PUBLIC_SUPABASE_URL", "")

    if public and next_public and public != next_public:
        report.append(
            "SUPABASE_PUBLIC_URL and NEXT_PUBLIC_SUPABASE_URL name different "
            "origins. They are the same browser-facing auth origin read by two "
            "consumers, so Open WebUI's discovery document and the web-console "
            "bundle would authenticate against different issuers"
        )
        failed = True
    if bool(public) != bool(next_public):
        report.append(
            "exactly one of SUPABASE_PUBLIC_URL and NEXT_PUBLIC_SUPABASE_URL is "
            "set, which is a half-moved cutover: whichever is unset falls back "
            "to an in-network gateway name no browser can resolve"
        )
        failed = True

    if public:
        # GoTrue builds BOTH the discovery document and the id_token issuer from
        # ENTERPRISE_AUTH_EXTERNAL_URL, while Open WebUI fetches that document
        # from SUPABASE_PUBLIC_URL + /auth/v1/.well-known/openid-configuration.
        # An OIDC client rejects a token whose issuer does not match the
        # document it read, so these two are one setting spelled twice.
        if external != public + "/auth/v1":
            report.append(
                "ENTERPRISE_AUTH_EXTERNAL_URL is not SUPABASE_PUBLIC_URL plus "
                "/auth/v1, so GoTrue advertises endpoints on one origin while "
                "the discovery document is fetched from another and the OIDC "
                "client rejects the issuer"
            )
            failed = True

        # A public browser-facing origin with a loopback SITE_URL is the live
        # broken state this check was written against: GoTrue sends the OAuth
        # consent redirect and every mail link to SITE_URL, so login walks the
        # user to their own machine.
        site = env.get("ENTERPRISE_SITE_URL", "")
        if not site:
            report.append(
                "ENTERPRISE_SITE_URL is unset while a public auth origin is "
                "configured, so GoTrue falls back to a loopback address and the "
                "OAuth consent page and every mail link resolve there"
            )
            failed = True
        elif re.search(r"//(localhost|127\.0\.0\.1|\[::1\])(:|/|$)", site):
            report.append(
                "ENTERPRISE_SITE_URL is a loopback address while a public auth "
                "origin is configured. It is GoTrue's SITE_URL, so the OAuth "
                "consent redirect and every mail link send the user to their own "
                "machine instead of the deployment"
            )
            failed = True

    # All three DSN variables, not just the one a hand cutover usually edits.
    # SUPABASE_DB_POOL_URL_LIBPQ is the flavour Open WebUI's SQLAlchemy actually
    # consumes, so it is the one where the short scheme does the damage; the
    # other two are checked because a deployment that sets them by hand will
    # have written all three the same way.
    for key in ("SUPABASE_DB_URL", "SUPABASE_DB_POOL_URL", "SUPABASE_DB_POOL_URL_LIBPQ"):
        if env.get(key, "").startswith("postgres://"):
            report.append(
                f"{key} uses the short postgres:// scheme. libpq and pgx accept "
                "it, SQLAlchemy does not, so Open WebUI's pgvector store fails "
                "with NoSuchModuleError. Use postgresql://"
            )
            failed = True

    for key in ("SUPABASE_DB_POOL_URL", "SUPABASE_DB_POOL_URL_LIBPQ"):
        value = env.get(key, "")
        if value and ":6543/" in value:
            report.append(
                f"{key} names port 6543, which exists only on a Supavisor pooler. "
                "A self-hosted Postgres serves every mode on 5432"
            )
            failed = True

    access, secret = env.get("S3_ACCESS_KEY", ""), env.get("S3_SECRET_KEY", "")
    if access and access == secret:
        report.append(
            "S3_ACCESS_KEY and S3_SECRET_KEY are the same value. SigV4 sends the "
            "access key id in the clear in the Authorization header, so the secret "
            "is disclosed on every request"
        )
        failed = True

    if not failed:
        report.append("coherently self-hosted, no hosted target left on a server-side variable")
    return (1 if failed else 0), report


# Fixture credentials are written as `${PASSWORD}` rather than a short literal:
# a secret scanner reads `postgres:pw@host` as PostgreSQL credentials and reports
# the whole diff, and a scanner that cries wolf on a fixture is one people learn
# to ignore. Nothing in this file parses a password.
# The browser-facing values name ONE origin, and it is the same origin the
# console app is served from, because that is where Caddyfile.console serves
# /auth/v1 from. A fixture that spelled them as a separate auth hostname would
# still satisfy every check here, but it would stop describing the deployment
# this file is run against.
GOOD_SELF_HOSTED = """
ENTERPRISE_DB_PASSWORD=x
ENTERPRISE_JWT_SECRET=y
ENTERPRISE_AUTH_EXTERNAL_URL=https://console.example.test/auth/v1
ENTERPRISE_SITE_URL=https://console.example.test
SUPABASE_URL=http://caddy-supabase
SUPABASE_PUBLIC_URL=https://console.example.test
NEXT_PUBLIC_SUPABASE_URL=https://console.example.test
SUPABASE_DB_URL=postgresql://postgres:${PASSWORD}@supabase-db:5432/postgres
SUPABASE_DB_POOL_URL=
SUPABASE_DB_POOL_URL_LIBPQ=
SUPABASE_JWT_ISSUER=https://console.example.test/auth/v1
SUPABASE_JWKS_URL=https://caddy-supabase/auth/v1/.well-known/jwks.json
SUPABASE_JWKS_CA_FILE=/etc/hive/supabase-ca/root.crt
S3_ENDPOINT=http://caddy-supabase/storage/v1/s3
S3_ACCESS_KEY=aaa
S3_SECRET_KEY=bbb
"""

GOOD_HOSTED = """
SUPABASE_URL=https://ref.supabase.co
SUPABASE_DB_URL=postgresql://user:${PASSWORD}@aws-1-x.pooler.supabase.com:5432/postgres
SUPABASE_JWKS_URL=https://ref.supabase.co/auth/v1/.well-known/jwks.json
S3_ENDPOINT=https://ref.supabase.co/storage/v1/s3
"""


def self_check() -> int:
    """Each mutation below is one way the cutover has actually gone wrong or could."""
    good = parse_env(GOOD_SELF_HOSTED)
    code, report = check(good)
    assert code == 0, report

    code, report = check(parse_env(GOOD_HOSTED))
    assert code == 0, report

    # A quoted value and an `export ` prefix must parse, or every check below is
    # vacuous on a real env file that uses either.
    quoted = parse_env('export SUPABASE_URL="http://caddy-supabase"\n')
    assert quoted == {"SUPABASE_URL": "http://caddy-supabase"}, quoted
    # A commented line is not a setting.
    assert parse_env("# SUPABASE_URL=https://ref.supabase.co\n") == {}

    mutations = {
        "stale hosted jwks url": {"SUPABASE_JWKS_URL": "https://ref.supabase.co/auth/v1/.well-known/jwks.json"},
        "stale hosted identity url": {"SUPABASE_URL": "https://ref.supabase.co"},
        "stale hosted db dsn": {"SUPABASE_DB_URL": "postgresql://user:${PASSWORD}@aws-1-x.pooler.supabase.com:5432/postgres"},
        "stale hosted s3 endpoint": {"S3_ENDPOINT": "https://ref.supabase.co/storage/v1/s3"},
        "stale hosted issuer": {"SUPABASE_JWT_ISSUER": "https://ref.supabase.co/auth/v1"},
        "plain http jwks": {"SUPABASE_JWKS_URL": "http://caddy-supabase/auth/v1/.well-known/jwks.json"},
        "jwks without a ca file": {"SUPABASE_JWKS_CA_FILE": ""},
        "issuer disagreement": {"SUPABASE_JWT_ISSUER": "http://supabase-auth:9999"},
        # The trailing-slash bypass itself: same origin, one side slashed.
        # Accepted by a comparison that stripped slashes, rejected by everything
        # at runtime.
        "issuer differing from the origin by one trailing slash": {
            "SUPABASE_JWT_ISSUER": "https://console.example.test/auth/v1/",
        },
        "origin differing from the issuer by one trailing slash": {
            "ENTERPRISE_AUTH_EXTERNAL_URL": "https://console.example.test/auth/v1/",
        },
        # A slash on BOTH sides, which the exact comparison alone cannot see:
        # there it is the concatenation that breaks, not the comparison.
        "trailing slash on both the issuer and the origin": {
            "SUPABASE_JWT_ISSUER": "https://console.example.test/auth/v1/",
            "ENTERPRISE_AUTH_EXTERNAL_URL": "https://console.example.test/auth/v1/",
        },
        # The half-moved browser origin, which is the shape the live deployment
        # was actually in: the data plane and the issuer had moved, and the two
        # values a browser reads had not.
        "browser origin left on the hosted project after the auth origin moved": {
            "SUPABASE_PUBLIC_URL": "https://ref.supabase.co",
            "NEXT_PUBLIC_SUPABASE_URL": "https://ref.supabase.co",
        },
        "only the bundle's origin moved": {
            "NEXT_PUBLIC_SUPABASE_URL": "https://other.example.test",
        },
        "the bundle's origin left unset": {"NEXT_PUBLIC_SUPABASE_URL": ""},
        "the discovery origin left unset": {"SUPABASE_PUBLIC_URL": ""},
        "auth origin on a different host from the browser origin": {
            "ENTERPRISE_AUTH_EXTERNAL_URL": "https://auth.example.test/auth/v1",
            "SUPABASE_JWT_ISSUER": "https://auth.example.test/auth/v1",
        },
        "auth origin missing its /auth/v1 prefix": {
            "ENTERPRISE_AUTH_EXTERNAL_URL": "https://console.example.test",
            "SUPABASE_JWT_ISSUER": "https://console.example.test",
        },
        "site url left on loopback beside a public auth origin": {
            "ENTERPRISE_SITE_URL": "http://localhost:3000",
        },
        "site url unset beside a public auth origin": {"ENTERPRISE_SITE_URL": ""},
        "trailing slash on the site url": {
            "ENTERPRISE_SITE_URL": "https://console.example.test/",
        },
        "trailing slash on the identity url": {"SUPABASE_URL": "http://caddy-supabase/"},
        "trailing slash on the browser-facing origin": {
            "SUPABASE_PUBLIC_URL": "https://auth.example.test/"
        },
        "trailing slash on the jwks url": {
            "SUPABASE_JWKS_URL": "https://caddy-supabase/auth/v1/.well-known/jwks.json/"
        },
        "trailing slash on the storage endpoint": {
            "S3_ENDPOINT": "http://caddy-supabase/storage/v1/s3/"
        },
        # The bypass: pin the issuer and leave the auth origin unset, so the
        # compose default is what GoTrue actually stamps.
        "issuer pinned against an unset auth origin": {
            "SUPABASE_JWT_ISSUER": "http://supabase-auth:9999",
            "ENTERPRISE_AUTH_EXTERNAL_URL": "",
        },

        "short dsn scheme": {"SUPABASE_DB_URL": "postgres://postgres:${PASSWORD}@supabase-db:5432/postgres"},
        "short dsn scheme on the pgx pool url": {
            "SUPABASE_DB_POOL_URL": "postgres://postgres:${PASSWORD}@supabase-db:5432/postgres"
        },
        "short dsn scheme on the libpq pool url": {
            "SUPABASE_DB_POOL_URL_LIBPQ": "postgres://postgres:${PASSWORD}@supabase-db:5432/postgres"
        },
        "invented pooler port": {"SUPABASE_DB_POOL_URL": "postgresql://postgres:${PASSWORD}@supabase-db:6543/postgres"},
        "s3 secret equals its own id": {"S3_SECRET_KEY": "aaa"},
    }
    for label, patch in mutations.items():
        env = dict(good)
        env.update(patch)
        code, report = check(env)
        assert code == 1, f"{label} did not fail: {report}"

    # Moving the auth origin while leaving SUPABASE_JWT_ISSUER unset is
    # COHERENT, not a mismatch, and this was written as a must-fail case first
    # and corrected by the assertion below refusing to fail. The compose
    # expression is ${SUPABASE_JWT_ISSUER:-${ENTERPRISE_AUTH_EXTERNAL_URL:-...}},
    # so an unset issuer inherits the moved origin and the two genuinely agree at
    # runtime. Only an issuer that is explicitly set to something else is the
    # failure this check is for.
    env = dict(good)
    env["SUPABASE_JWT_ISSUER"] = ""
    code, report = check(env)
    assert code == 0, report

    # Moving the whole browser-facing origin together is coherent, and this
    # case exists so the half-move mutations above cannot be satisfied by a
    # check that simply refuses any change to these values.
    env = dict(good)
    for key in ("SUPABASE_PUBLIC_URL", "NEXT_PUBLIC_SUPABASE_URL"):
        env[key] = "https://console.other.test"
    for key in ("ENTERPRISE_AUTH_EXTERNAL_URL", "SUPABASE_JWT_ISSUER"):
        env[key] = "https://console.other.test/auth/v1"
    env["ENTERPRISE_SITE_URL"] = "https://console.other.test"
    code, report = check(env)
    assert code == 0, report

    # A browser-facing public origin naming the HOSTED project is still not a
    # mixed state in the server-side sense this file was originally written for:
    # SUPABASE_PUBLIC_URL and NEXT_PUBLIC_SUPABASE_URL stay out of SERVER_SIDE.
    # It is now refused for a different and narrower reason, that it disagrees
    # with the auth origin GoTrue stamps, so the case is kept as a must-fail
    # with the reason it fails asserted rather than deleted. Superseding a
    # passing case with a failing one is a deliberate semantics change, not an
    # accident: a staged cutover that leaves the browser on the old project
    # while the issuer has moved cannot log anyone in.
    env = dict(good)
    env["NEXT_PUBLIC_SUPABASE_URL"] = "https://ref.supabase.co"
    env["SUPABASE_PUBLIC_URL"] = "https://ref.supabase.co"
    code, report = check(env)
    assert code == 1, report
    assert any("ENTERPRISE_AUTH_EXTERNAL_URL is not SUPABASE_PUBLIC_URL" in r for r in report), report
    assert not any("MIXED STATE" in r for r in report), report

    print(f"check-env-supabase-target self-check: OK ({len(mutations) + 6} cases)")
    return 0


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("env_file", nargs="?", help="path to the env file to check")
    parser.add_argument("--self-check", action="store_true")
    args = parser.parse_args()

    if args.self_check:
        return self_check()
    if not args.env_file:
        parser.error("give an env file, or --self-check")

    path = Path(args.env_file)
    if not path.exists():
        print(f"check-env-supabase-target: no such file: {path}")
        return 1
    code, report = check(parse_env(path.read_text()))
    for line in report:
        print(f"  {line}")
    print(f"check-env-supabase-target: {'FAIL' if code else 'ok'} ({path})")
    return code


if __name__ == "__main__":
    sys.exit(main())

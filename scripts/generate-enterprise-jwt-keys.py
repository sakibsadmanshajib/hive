#!/usr/bin/env python3
"""Generate the asymmetric JWT signing material the enterprise profile needs.

Why this exists
---------------
docker-compose.enterprise.yml configured GoTrue with a symmetric
GOTRUE_JWT_SECRET and nothing else. GoTrue's JWKS endpoint deliberately
excludes symmetric keys: internal/api/jwks.go skips every key whose public
half is nil or of type jwa.OctetSeq, so /.well-known/jwks.json answered with
an empty key set. apps/edge-api validates tokens with jwt.WithKeySet against
exactly that endpoint and refuses to boot when the initial refresh yields no
usable key (apps/edge-api/internal/auth/jwt_supabase.go), so a self-hosted
stack configured that way could not authenticate a single request.

The fix is upstream-supported and config-only: hand GoTrue an EC P-256
signing key through GOTRUE_JWT_KEYS. GoTrue then signs access tokens with
ES256, publishes the matching public key on its JWKS endpoint, and keeps
accepting the legacy HS256 anon and service_role keys through the
GOTRUE_JWT_SECRET fallback in conf.FindPublicKeyByKid.

This script prints two values:

  ENTERPRISE_JWT_KEYS   the signing set, for GoTrue only. Carries the EC
                        private scalar and the legacy symmetric key. Secret.
  ENTERPRISE_JWT_JWKS   the verification set, for PostgREST. Carries the EC
                        public key plus the legacy symmetric key so PostgREST
                        can verify both the new ES256 user tokens and the
                        existing HS256 anon and service_role keys. The
                        symmetric entry is the HMAC secret in another
                        encoding, so this value is secret too.

Both go in .env, which is never committed. Neither is ever logged.

Usage: run with ENTERPRISE_JWT_SECRET set in the environment to your existing
enterprise JWT secret. Add --self-check to run the offline guards instead of
generating anything.

Rotation: GoTrue accepts exactly one signing key (conf.JwtKeysDecoder.Validate
rejects zero or more than one key carrying the "sign" operation), so rotation
is a replace, not an append. Tokens signed by the old key stop validating once
the old public key leaves the JWKS, which is a forced re-login, not data loss.
"""

import argparse
import base64
import json
import os
import subprocess
import sys
import uuid

CURVE = "prime256v1"  # NIST P-256, the curve behind ES256.
COORD_BYTES = 32  # P-256 field element width.


def b64url(raw):
    """base64url without padding, per RFC 7515 section 2."""
    return base64.urlsafe_b64encode(raw).rstrip(b"=").decode("ascii")


def run_openssl(args, stdin=None):
    """Run openssl and return stdout, raising a readable message on failure."""
    try:
        proc = subprocess.run(
            ["openssl"] + list(args),
            input=stdin,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            check=True,
        )
    except FileNotFoundError:
        raise SystemExit("openssl not found on PATH; install it and re-run") from None
    except subprocess.CalledProcessError as exc:
        detail = exc.stderr.decode("utf-8", "replace").strip()
        raise SystemExit("openssl failed: " + detail) from None
    return proc.stdout


def parse_openssl_ec_text(text):
    """Pull the private scalar and the uncompressed public point out of
    `openssl ec -text -noout` output.

    The output labels two hex blocks, priv and pub, each continued over
    indented lines. The private scalar may carry a leading zero byte because
    openssl prints it as a signed integer, and it may be shorter than the
    field width when its top bytes are zero, so it is normalised to exactly
    32 bytes rather than trusted as printed.
    """
    section = None
    chunks = {"priv": [], "pub": []}
    for line in text.splitlines():
        stripped = line.strip()
        if stripped.startswith("priv:"):
            section = "priv"
            stripped = stripped[len("priv:"):].strip()
        elif stripped.startswith("pub:"):
            section = "pub"
            stripped = stripped[len("pub:"):].strip()
        elif not line.startswith(" ") or ":" not in stripped:
            # A non-indented line, or an "ASN1 OID: prime256v1" style label,
            # ends the current hex block.
            section = None
            continue
        if section and stripped:
            chunks[section].append(stripped)

    def to_bytes(name):
        joined = "".join(chunks[name]).replace(":", "").replace(" ", "")
        if not joined:
            raise SystemExit("could not read the " + name + " block from openssl")
        return bytes.fromhex(joined)

    priv = to_bytes("priv").lstrip(b"\x00").rjust(COORD_BYTES, b"\x00")
    pub = to_bytes("pub")
    if len(priv) != COORD_BYTES:
        raise SystemExit("private scalar has the wrong width")
    if len(pub) != 1 + 2 * COORD_BYTES or pub[0] != 0x04:
        raise SystemExit("public point is not an uncompressed P-256 point")
    return priv, pub


def generate_ec_jwk(kid):
    """Return (private JWK, public JWK) for a fresh P-256 keypair."""
    pem = run_openssl(["ecparam", "-name", CURVE, "-genkey", "-noout"])
    text = run_openssl(["ec", "-text", "-noout"], stdin=pem).decode("utf-8", "replace")
    priv, pub = parse_openssl_ec_text(text)
    x = pub[1 : 1 + COORD_BYTES]
    y = pub[1 + COORD_BYTES :]

    public_jwk = {
        "kty": "EC",
        "crv": "P-256",
        "x": b64url(x),
        "y": b64url(y),
        "alg": "ES256",
        "kid": kid,
        "use": "sig",
        "key_ops": ["verify"],
    }
    # GoTrue picks its signing key by looking for the "sign" operation
    # (conf.GetSigningJwk) and picks the algorithm off the "alg" field
    # (conf.GetSigningAlg), which falls back to HS256 when "alg" is absent.
    # Both fields are load bearing, not decoration.
    private_jwk = {
        "kty": "EC",
        "crv": "P-256",
        "x": public_jwk["x"],
        "y": public_jwk["y"],
        "d": b64url(priv),
        "alg": "ES256",
        "kid": kid,
        "key_ops": ["sign", "verify"],
    }
    return private_jwk, public_jwk


def legacy_symmetric_jwk(secret):
    """The existing GOTRUE_JWT_SECRET expressed as an oct JWK.

    This goes in BOTH printed values, for two different reasons.

    In GOTRUE_JWT_KEYS, because once GoTrue is given a key set it validates
    incoming tokens against that set alone. Observed directly on v2.189.0: with
    only the EC key configured, GoTrue answers every admin call made with the
    HS256 service_role key "signing method HS256 is invalid" (HTTP 403), which
    breaks the seeding scripts, the invite flow and every other admin path.
    Carrying the legacy key here, verify-only, restores them.

    In the verification set, because PostgREST is handed a JWKS instead of a
    raw secret and would otherwise lose the ability to check those same tokens.

    No kid is set: the legacy tokens carry no kid header, and a keyed entry
    could not match them. key_ops is verify only, never sign, so it cannot
    become a second signing key, which GoTrue rejects outright.
    """
    return {
        "kty": "oct",
        "k": b64url(secret.encode("utf-8")),
        "alg": "HS256",
        "key_ops": ["verify"],
    }


def build(secret):
    kid = str(uuid.uuid4())
    private_jwk, public_jwk = generate_ec_jwk(kid)
    legacy = legacy_symmetric_jwk(secret)
    keys = json.dumps([private_jwk, legacy], separators=(",", ":"))
    jwks = json.dumps({"keys": [public_jwk, legacy]}, separators=(",", ":"))
    return keys, jwks


def self_check():
    """Offline guards. No network, no framework, no files written."""
    secret = "self-check-secret-that-is-long-enough-for-hs256"
    keys_raw, jwks_raw = build(secret)
    keys = json.loads(keys_raw)
    jwks = json.loads(jwks_raw)

    assert isinstance(keys, list) and len(keys) == 2, "JWT_KEYS carries the EC key and the legacy key"
    private_jwk = keys[0]

    # The legacy key has to travel with the signing key or GoTrue stops
    # accepting the HS256 anon and service_role keys entirely.
    legacy_in_keys = [k for k in keys if k["kty"] == "oct"]
    assert len(legacy_in_keys) == 1, "the legacy symmetric key must be in JWT_KEYS"
    assert legacy_in_keys[0]["key_ops"] == ["verify"], "the legacy key must never sign"
    assert base64.urlsafe_b64decode(legacy_in_keys[0]["k"] + "==").decode("utf-8") == secret

    # GoTrue rejects a key set with no signing key or with more than one.
    signing = [k for k in keys if "sign" in k.get("key_ops", [])]
    assert len(signing) == 1, "expected exactly one signing key"

    assert private_jwk["alg"] == "ES256", "alg must be ES256 or GoTrue falls back to HS256"
    assert private_jwk["kid"], "GoTrue indexes keys by kid; an empty kid collides"
    for field in ("d", "x", "y"):
        raw = base64.urlsafe_b64decode(private_jwk[field] + "==")
        assert len(raw) == COORD_BYTES, field + " has the wrong width"

    # The verification set must never carry private material.
    ec_public = [k for k in jwks["keys"] if k["kty"] == "EC"]
    assert len(ec_public) == 1, "expected exactly one EC entry in the verification set"
    assert "d" not in ec_public[0], "private scalar leaked into the verification set"
    assert ec_public[0]["kid"] == private_jwk["kid"], "public and private kid must match"
    assert ec_public[0]["x"] == private_jwk["x"] and ec_public[0]["y"] == private_jwk["y"]

    # The legacy symmetric entry has to round-trip the existing secret, or
    # every HS256 anon and service_role key stops validating at PostgREST.
    oct_keys = [k for k in jwks["keys"] if k["kty"] == "oct"]
    assert len(oct_keys) == 1, "expected exactly one symmetric entry"
    assert base64.urlsafe_b64decode(oct_keys[0]["k"] + "==").decode("utf-8") == secret
    assert "kid" not in oct_keys[0], "legacy tokens carry no kid; a keyed entry cannot match"

    # Two runs must not produce the same key.
    other_keys, _ = build(secret)
    assert json.loads(other_keys)[0]["d"] != private_jwk["d"], "keypair is not fresh"

    # A short private scalar (leading zero bytes) must still normalise to 32.
    priv, pub = parse_openssl_ec_text(
        "Private-Key: (256 bit)\npriv:\n    00:01:02\npub:\n    04:"
        + ":".join(["11"] * 64)
        + "\nASN1 OID: prime256v1\n"
    )
    assert priv == b"\x00" * 30 + b"\x01\x02", "short private scalar was not left-padded"
    assert len(pub) == 65 and pub[0] == 0x04

    print("generate-enterprise-jwt-keys self-check: OK")
    return 0


def main():
    parser = argparse.ArgumentParser(description="Generate enterprise JWT key material")
    parser.add_argument(
        "--self-check",
        action="store_true",
        help="run the offline guards and exit; generates nothing for use",
    )
    args = parser.parse_args()
    if args.self_check:
        return self_check()

    secret = os.environ.get("ENTERPRISE_JWT_SECRET", "").strip()
    if len(secret) < 32:
        print(
            "ENTERPRISE_JWT_SECRET must be set to your existing enterprise JWT "
            "secret (at least 32 characters) so the legacy anon and service_role "
            "keys keep validating. Generate one with: openssl rand -base64 48",
            file=sys.stderr,
        )
        return 2

    keys, jwks = build(secret)
    print("# Paste both lines into .env. Secret material: never commit, never log.")
    print("ENTERPRISE_JWT_KEYS=" + keys)
    print("ENTERPRISE_JWT_JWKS=" + jwks)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

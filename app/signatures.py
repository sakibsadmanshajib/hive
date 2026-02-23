from __future__ import annotations

import hashlib
import hmac
import time
from typing import Mapping


def verify_hmac_sha256_signature(signature: str, body: bytes | str, secret: str) -> bool:
    if isinstance(body, str):
        body_bytes = body.encode("utf-8")
    else:
        body_bytes = body

    expected = hmac.new(secret.encode("utf-8"), body_bytes, hashlib.sha256).hexdigest()
    return hmac.compare_digest(signature, expected)


def is_timestamp_within_tolerance(
    timestamp: str | int,
    *,
    now: int | None = None,
    tolerance_seconds: int = 300,
) -> bool:
    try:
        timestamp_value = int(timestamp)
    except (TypeError, ValueError):
        return False

    if now is None:
        now = int(time.time())

    return abs(now - timestamp_value) <= tolerance_seconds


def verify_provider_signature(
    headers: Mapping[str, str],
    body: bytes | str,
    secret: str,
    *,
    signature_header: str = "X-Signature",
    timestamp_header: str = "X-Timestamp",
    tolerance_seconds: int = 300,
    now: int | None = None,
) -> bool:
    signature = headers.get(signature_header)
    timestamp = headers.get(timestamp_header)
    if not signature or timestamp is None:
        return False

    if not verify_hmac_sha256_signature(signature, body, secret):
        return False

    return is_timestamp_within_tolerance(timestamp, now=now, tolerance_seconds=tolerance_seconds)


def verify_bkash_signature(headers: Mapping[str, str], body: bytes | str, secret: str) -> bool:
    return verify_provider_signature(
        headers,
        body,
        secret,
        signature_header="X-BKash-Signature",
        timestamp_header="X-BKash-Timestamp",
    )


def verify_sslcommerz_signature(payload: Mapping[str, str], expected_hash: str) -> bool:
    canonical = "|".join(f"{key}={payload[key]}" for key in sorted(payload.keys()))
    digest = hashlib.sha256(canonical.encode("utf-8")).hexdigest()
    return hmac.compare_digest(digest, expected_hash)

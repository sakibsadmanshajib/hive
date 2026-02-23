import hashlib
import hmac
import time
import unittest

from app.signatures import (
    verify_bkash_signature,
    verify_hmac_sha256_signature,
    verify_provider_signature,
    verify_sslcommerz_signature,
)


class SignatureVerificationTests(unittest.TestCase):
    def test_verify_hmac_sha256_signature_passes_with_valid_signature(self) -> None:
        body = b'{"transaction_id":"tx-1"}'
        secret = "test-secret"
        signature = hmac.new(secret.encode("utf-8"), body, hashlib.sha256).hexdigest()

        self.assertTrue(verify_hmac_sha256_signature(signature, body, secret))

    def test_verify_hmac_sha256_signature_fails_with_invalid_signature(self) -> None:
        body = b'{"transaction_id":"tx-1"}'

        self.assertFalse(verify_hmac_sha256_signature("bad-signature", body, "test-secret"))

    def test_verify_provider_signature_fails_for_replay_window(self) -> None:
        body = b'{"transaction_id":"tx-2"}'
        secret = "test-secret"
        headers = {
            "X-Signature": hmac.new(secret.encode("utf-8"), body, hashlib.sha256).hexdigest(),
            "X-Timestamp": "100",
        }

        self.assertFalse(
            verify_provider_signature(headers, body, secret, now=1000, tolerance_seconds=300),
        )

    def test_verify_bkash_signature_passes_with_valid_headers(self) -> None:
        body = b'{"paymentID":"p-1"}'
        secret = "bkash-secret"
        now = str(int(time.time()))
        headers = {
            "X-BKash-Signature": hmac.new(secret.encode("utf-8"), body, hashlib.sha256).hexdigest(),
            "X-BKash-Timestamp": now,
        }

        self.assertTrue(verify_bkash_signature(headers, body, secret))

    def test_verify_bkash_signature_fails_for_bad_signature(self) -> None:
        now = str(int(time.time()))
        headers = {
            "X-BKash-Signature": "wrong",
            "X-BKash-Timestamp": now,
        }

        self.assertFalse(verify_bkash_signature(headers, b'{"paymentID":"p-2"}', "bkash-secret"))

    def test_verify_sslcommerz_signature_passes_and_fails_deterministically(self) -> None:
        payload = {"amount": "100.00", "currency": "BDT", "tran_id": "T-1"}
        canonical = "amount=100.00|currency=BDT|tran_id=T-1"
        expected_hash = hashlib.sha256(canonical.encode("utf-8")).hexdigest()

        self.assertTrue(verify_sslcommerz_signature(payload, expected_hash))
        self.assertFalse(verify_sslcommerz_signature(payload, "wrong-hash"))


if __name__ == "__main__":
    unittest.main()

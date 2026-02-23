import json
import unittest

from app.api import GatewayApp


class WebhookSecurityTests(unittest.TestCase):
    def test_bkash_webhook_rejects_invalid_signature(self) -> None:
        app = GatewayApp()
        status, _headers, body = app.handle(
            "POST",
            "/v1/payments/webhook",
            headers={"x-provider": "bkash", "X-BKash-Timestamp": "1700000000", "X-BKash-Signature": "bad"},
            body=json.dumps(
                {
                    "provider": "bkash",
                    "provider_txn_id": "tx-1",
                    "intent_id": "intent-1",
                    "verified": True,
                }
            ),
        )
        self.assertEqual(status, 401)
        self.assertIn("invalid signature", body)


if __name__ == "__main__":
    unittest.main()

import json
import unittest

from app.api import GatewayApp


class ApiTests(unittest.TestCase):
    def setUp(self) -> None:
        self.app = GatewayApp()
        self.app.ledger.mint_purchased("user-1", 1000, payment_id="seed")

    def test_models_endpoint(self) -> None:
        status, _headers, body = self.app.handle("GET", "/v1/models", headers={}, body=None)
        self.assertEqual(status, 200)
        payload = json.loads(body)
        self.assertTrue(payload["data"])

    def test_chat_completion_charges_credits(self) -> None:
        status, headers, body = self.app.handle(
            "POST",
            "/v1/chat/completions",
            headers={"x-api-key": "dev-user-1"},
            body=json.dumps(
                {
                    "model": "auto",
                    "task_type": "chat",
                    "messages": [{"role": "user", "content": "hello"}],
                }
            ),
        )

        self.assertEqual(status, 200)
        self.assertIn("x-model-routed", headers)
        self.assertIn("x-actual-credits", headers)
        payload = json.loads(body)
        self.assertEqual(payload["object"], "chat.completion")


if __name__ == "__main__":
    unittest.main()

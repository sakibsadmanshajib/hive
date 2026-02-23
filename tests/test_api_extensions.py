import json
import unittest

from app.api import GatewayApp


class ApiExtensionsTests(unittest.TestCase):
    def setUp(self) -> None:
        self.app = GatewayApp()
        self.app.ledger.mint_purchased("user-1", 5000, payment_id="seed")

    def test_images_generation_endpoint(self) -> None:
        status, headers, body = self.app.handle(
            "POST",
            "/v1/images/generations",
            headers={"x-api-key": "dev-user-1"},
            body=json.dumps({"prompt": "a tea shop in dhaka"}),
        )
        self.assertEqual(status, 200)
        self.assertIn("x-actual-credits", headers)
        payload = json.loads(body)
        self.assertEqual(payload["object"], "list")

    def test_responses_endpoint(self) -> None:
        status, _headers, body = self.app.handle(
            "POST",
            "/v1/responses",
            headers={"x-api-key": "dev-user-1"},
            body=json.dumps({"input": "Say hello"}),
        )
        self.assertEqual(status, 200)
        payload = json.loads(body)
        self.assertEqual(payload["object"], "response")


if __name__ == "__main__":
    unittest.main()

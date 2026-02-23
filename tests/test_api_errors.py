import json
import unittest

from app.api import GatewayApp


class ApiErrorTests(unittest.TestCase):
    def test_chat_returns_402_when_insufficient_credits(self) -> None:
        app = GatewayApp()
        status, _headers, body = app.handle(
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
        self.assertEqual(status, 402)
        self.assertIn("insufficient credits", body)


if __name__ == "__main__":
    unittest.main()

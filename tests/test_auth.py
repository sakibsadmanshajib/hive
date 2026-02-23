import unittest

from app.auth import ApiKeyService


class ApiKeyServiceTests(unittest.TestCase):
    def setUp(self) -> None:
        self.service = ApiKeyService()

    def test_validate_key_allows_required_scope(self) -> None:
        key = self.service.issue_key("user-1", ["read", "write"])

        result = self.service.validate_key(key, "read")

        self.assertEqual(result, "user-1")

    def test_validate_key_rejects_missing_scope(self) -> None:
        key = self.service.issue_key("user-2", ["read"])

        result = self.service.validate_key(key, "write")

        self.assertIsNone(result)

    def test_validate_key_rejects_revoked_key(self) -> None:
        key = self.service.issue_key("user-3", ["read"])
        self.service.revoke_key(key)

        result = self.service.validate_key(key, "read")

        self.assertIsNone(result)


if __name__ == "__main__":
    unittest.main()

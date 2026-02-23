import unittest

from app.ratelimit import InMemoryRateLimiter


class RateLimitTests(unittest.TestCase):
    def test_blocks_after_limit_exceeded(self) -> None:
        limiter = InMemoryRateLimiter(limit=2, window_seconds=60)
        self.assertTrue(limiter.allow("key-1"))
        self.assertTrue(limiter.allow("key-1"))
        self.assertFalse(limiter.allow("key-1"))


if __name__ == "__main__":
    unittest.main()

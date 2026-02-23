from __future__ import annotations

import time
from collections import defaultdict


class InMemoryRateLimiter:
    def __init__(self, limit: int, window_seconds: int) -> None:
        self.limit = limit
        self.window_seconds = window_seconds
        self.events: dict[str, list[float]] = defaultdict(list)

    def allow(self, key: str) -> bool:
        now = time.time()
        cutoff = now - self.window_seconds
        valid = [ts for ts in self.events[key] if ts >= cutoff]
        if len(valid) >= self.limit:
            self.events[key] = valid
            return False
        valid.append(now)
        self.events[key] = valid
        return True

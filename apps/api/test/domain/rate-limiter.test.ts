import { describe, expect, it } from "vitest";

import { InMemoryRateLimiter } from "../../src/domain/rate-limiter";

describe("InMemoryRateLimiter", () => {
  it("blocks after limit is exceeded", () => {
    const times = [1000, 1001, 1002];
    const limiter = new InMemoryRateLimiter(2, 60, () => times.shift() ?? 0);

    expect(limiter.allow("key-1")).toBe(true);
    expect(limiter.allow("key-1")).toBe(true);
    expect(limiter.allow("key-1")).toBe(false);
  });

  it("allows again after window passes", () => {
    const times = [1000, 1001, 1062];
    const limiter = new InMemoryRateLimiter(2, 60, () => times.shift() ?? 0);

    expect(limiter.allow("key-1")).toBe(true);
    expect(limiter.allow("key-1")).toBe(true);
    expect(limiter.allow("key-1")).toBe(true);
  });
});

export class InMemoryRateLimiter {
  private readonly limit: number;
  private readonly windowSeconds: number;
  private readonly nowFn: () => number;
  private readonly events = new Map<string, number[]>();

  constructor(limit: number, windowSeconds: number, nowFn?: () => number) {
    this.limit = limit;
    this.windowSeconds = windowSeconds;
    this.nowFn = nowFn ?? (() => Date.now() / 1000);
  }

  allow(key: string): boolean {
    const now = this.nowFn();
    const cutoff = now - this.windowSeconds;
    const existing = this.events.get(key) ?? [];
    const valid = existing.filter((timestamp) => timestamp >= cutoff);
    if (valid.length >= this.limit) {
      this.events.set(key, valid);
      return false;
    }

    valid.push(now);
    this.events.set(key, valid);
    return true;
  }
}

import Redis from "ioredis";

export class RedisRateLimiter {
  private readonly redis: Redis;
  private readonly limit: number;
  private readonly windowSeconds: number;

  constructor(redisUrl: string, limit: number, windowSeconds = 60) {
    this.redis = new Redis(redisUrl, { lazyConnect: true, maxRetriesPerRequest: 1 });
    this.limit = limit;
    this.windowSeconds = windowSeconds;
  }

  async allow(key: string): Promise<boolean> {
    try {
      if (this.redis.status === "wait") {
        await this.redis.connect();
      }
      const bucket = `ratelimit:${key}`;
      const count = await this.redis.incr(bucket);
      if (count === 1) {
        await this.redis.expire(bucket, this.windowSeconds);
      }
      return count <= this.limit;
    } catch {
      return true;
    }
  }
}

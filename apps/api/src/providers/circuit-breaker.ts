import type { ProviderName } from "./types";

export type CircuitState = "CLOSED" | "OPEN" | "HALF_OPEN";

export interface CircuitBreakerConfig {
  failureThreshold: number;
  resetTimeoutMs: number;
}

export class CircuitBreaker {
  private failures = 0;
  private lastFailureTime?: number;
  private state: CircuitState = "CLOSED";

  constructor(
    public readonly providerName: ProviderName,
    private readonly config: CircuitBreakerConfig,
  ) {}

  isOpen(): boolean {
    if (this.state === "OPEN") {
      if (this.lastFailureTime && Date.now() - this.lastFailureTime > this.config.resetTimeoutMs) {
        this.state = "HALF_OPEN";
        return false;
      }
      return true;
    }
    return false;
  }

  recordSuccess(): void {
    this.failures = 0;
    this.lastFailureTime = undefined;
    this.state = "CLOSED";
  }

  recordFailure(): void {
    this.failures++;
    this.lastFailureTime = Date.now();
    if (this.failures >= this.config.failureThreshold) {
      this.state = "OPEN";
    }
  }

  getState(): CircuitState {
    this.isOpen(); // Trigger transition to HALF_OPEN if timeout expired
    return this.state;
  }

  getFailures(): number {
    return this.failures;
  }
}

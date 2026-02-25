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
  private probeInFlight = false;

  constructor(
    public readonly providerName: ProviderName,
    private readonly config: CircuitBreakerConfig,
  ) {}

  /**
   * Evaluates if state should transition (e.g. from OPEN to HALF_OPEN after timeout).
   * Should be called before checking isOpen() or tryAcquireProbe().
   */
  evaluateState(): void {
    if (this.state === "OPEN") {
      if (this.lastFailureTime && Date.now() - this.lastFailureTime > this.config.resetTimeoutMs) {
        this.state = "HALF_OPEN";
        this.probeInFlight = false;
      }
    }
  }

  /**
   * Returns true if the circuit is OPEN (blocked).
   * Note: Does NOT return true for HALF_OPEN, but you must still acquire a probe.
   */
  isOpen(): boolean {
    return this.state === "OPEN";
  }

  isHalfOpen(): boolean {
    return this.state === "HALF_OPEN";
  }

  /**
   * In HALF_OPEN state, only one probe is allowed.
   * Returns true if a probe can be performed.
   */
  tryAcquireProbe(): boolean {
    if (this.state === "HALF_OPEN" && !this.probeInFlight) {
      this.probeInFlight = true;
      return true;
    }
    return false;
  }

  recordSuccess(): void {
    this.failures = 0;
    this.lastFailureTime = undefined;
    this.state = "CLOSED";
    this.probeInFlight = false;
  }

  recordFailure(): void {
    this.failures++;
    this.lastFailureTime = Date.now();
    this.probeInFlight = false;
    if (this.failures >= this.config.failureThreshold) {
      this.state = "OPEN";
    }
  }

  getState(): CircuitState {
    return this.state;
  }

  getFailures(): number {
    return this.failures;
  }
}

import type { ProviderName } from "./types";

/**
 * Represents the current state of the circuit breaker.
 */
export type CircuitState = "CLOSED" | "OPEN" | "HALF_OPEN";

/**
 * Configuration for the circuit breaker.
 */
export interface CircuitBreakerConfig {
  /**
   * Number of consecutive failures before tripping the circuit.
   */
  failureThreshold: number;
  /**
   * How long (in ms) to stay in OPEN state before trying again.
   */
  resetTimeoutMs: number;
}

/**
 * Implements a circuit breaker pattern to protect against cascading provider failures.
 *
 * The circuit transitions from CLOSED to OPEN after a threshold of consecutive failures.
 * After a timeout, it transitions to HALF_OPEN, allowing a single probe request.
 * Success in HALF_OPEN resets the circuit to CLOSED, while failure trips it back to OPEN.
 */
export class CircuitBreaker {
  private failures = 0;
  private lastFailureTime?: number;
  private state: CircuitState = "CLOSED";
  private probeInFlight = false;

  /**
   * Creates a new CircuitBreaker instance.
   * @param providerName - The name of the provider being guarded.
   * @param config - Thresholds and timeout configuration.
   */
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
   * Returns true if the circuit is currently OPEN (blocked).
   * Note: Returns false for HALF_OPEN, but you must still acquire a probe via tryAcquireProbe().
   */
  isOpen(): boolean {
    return this.state === "OPEN";
  }

  /**
   * Returns true if the circuit is currently in HALF_OPEN state.
   */
  isHalfOpen(): boolean {
    return this.state === "HALF_OPEN";
  }

  /**
   * In HALF_OPEN state, only one probe is allowed concurrently.
   * Returns true if a probe can be performed, false otherwise.
   */
  tryAcquireProbe(): boolean {
    if (this.state === "HALF_OPEN" && !this.probeInFlight) {
      this.probeInFlight = true;
      return true;
    }
    return false;
  }

  /**
   * Records a successful operation, resetting the failure count and closing the circuit.
   */
  recordSuccess(): void {
    this.failures = 0;
    this.lastFailureTime = undefined;
    this.state = "CLOSED";
    this.probeInFlight = false;
  }

  /**
   * Records a failed operation, incrementing the failure count and potentially tripping the circuit.
   */
  recordFailure(): void {
    this.failures++;
    this.lastFailureTime = Date.now();
    this.probeInFlight = false;
    if (this.failures >= this.config.failureThreshold) {
      this.state = "OPEN";
    }
  }

  /**
   * Returns the current state of the circuit.
   */
  getState(): CircuitState {
    return this.state;
  }

  /**
   * Returns the current consecutive failure count.
   */
  getFailures(): number {
    return this.failures;
  }
}

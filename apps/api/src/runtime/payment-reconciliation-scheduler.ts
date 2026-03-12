import type { PaymentReconciliationResult } from "../domain/types";
import type { PaymentReconciliationService } from "./payment-reconciliation";

type SchedulerLogger = {
  warn(payload: Record<string, unknown>, message?: string): void;
  error(payload: Record<string, unknown>, message?: string): void;
};

type SchedulerConfig = {
  intervalMs: number;
  lookbackHours: number;
};

export class PaymentReconciliationScheduler {
  private timer?: ReturnType<typeof setInterval>;
  private inFlight = false;

  constructor(
    private readonly reconciliation: Pick<PaymentReconciliationService, "reconcileRecentPayments">,
    private readonly logger: SchedulerLogger,
    private readonly config: SchedulerConfig,
  ) { }

  start(): void {
    if (this.timer) {
      return;
    }

    this.timer = setInterval(() => {
      void this.runOnce();
    }, this.config.intervalMs);
  }

  stop(): void {
    if (!this.timer) {
      return;
    }
    clearInterval(this.timer);
    this.timer = undefined;
  }

  private async runOnce(): Promise<void> {
    if (this.inFlight) {
      return;
    }

    this.inFlight = true;
    try {
      const result = await this.reconciliation.reconcileRecentPayments(this.config.lookbackHours);
      this.logDrift(result);
    } catch (error) {
      this.logger.error(
        {
          err: error,
          lookbackHours: this.config.lookbackHours,
        },
        "payment reconciliation job failed",
      );
    } finally {
      this.inFlight = false;
    }
  }

  private logDrift(result: PaymentReconciliationResult): void {
    if (result.summary.totalFindings === 0) {
      return;
    }

    this.logger.warn(
      {
        summary: result.summary,
        findings: result.findings,
      },
      "payment reconciliation drift detected",
    );
  }
}

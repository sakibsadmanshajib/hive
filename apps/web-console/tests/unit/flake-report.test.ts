import { describe, expect, it } from "vitest";
import {
  buildFlakeReport,
  MAX_FLAKY_TESTS,
  type FlakeEntry,
} from "../e2e/support/flake-report";

function entries(
  spec: readonly FlakeEntry["outcome"][]
): readonly FlakeEntry[] {
  return spec.map((outcome, i) => ({
    file: "e2e/example.spec.ts",
    title: `test ${i}`,
    outcome,
    attempts: outcome === "flaky" ? 3 : 1,
  }));
}

function repeat(
  outcome: FlakeEntry["outcome"],
  n: number
): readonly FlakeEntry["outcome"][] {
  return Array.from({ length: n }, () => outcome);
}

describe("buildFlakeReport", () => {
  it("passes a run where nothing needed a retry", () => {
    const report = buildFlakeReport(entries(repeat("expected", 26)));

    expect(report.executed).toBe(26);
    expect(report.flaky).toBe(0);
    expect(report.ratePct).toBe(0);
    expect(report.gateTripped).toBe(false);
    expect(report.markdown).toContain("No test needed a retry.");
  });

  it("fails the run on the 7.7 percent measured in CI job 31361681115", () => {
    // 34 collected, 6 skipped, 2 failed, 2 that passed only on their third
    // attempt. The job log read "24 passed" and said nothing about the two.
    const report = buildFlakeReport(
      entries([
        ...repeat("expected", 22),
        ...repeat("flaky", 2),
        ...repeat("unexpected", 2),
        ...repeat("skipped", 6),
      ])
    );

    expect(report.executed).toBe(26);
    expect(report.flaky).toBe(2);
    expect(report.failed).toBe(2);
    expect(report.ratePct).toBe(7.7);
    expect(report.gateTripped).toBe(true);
    expect(report.markdown).toContain("Failing this run for flakiness");
  });

  it("trips on a single retry-pass, which is what a zero threshold means", () => {
    const report = buildFlakeReport(
      entries([...repeat("expected", 25), "flaky"])
    );

    expect(MAX_FLAKY_TESTS).toBe(0);
    expect(report.flaky).toBe(1);
    expect(report.ratePct).toBe(3.8);
    expect(report.gateTripped).toBe(true);
  });

  it("keeps skipped tests out of the denominator", () => {
    // 1 flaky of 2 executed is 50 percent, not 10 percent of 10 collected.
    const report = buildFlakeReport(
      entries([...repeat("skipped", 8), "expected", "flaky"])
    );

    expect(report.executed).toBe(2);
    expect(report.ratePct).toBe(50);
  });

  it("names every retry-pass with its spec and attempt count", () => {
    const report = buildFlakeReport([
      {
        file: "e2e/profile-completion.spec.ts",
        title: "profile completion > setup saves profile",
        outcome: "flaky",
        attempts: 3,
      },
      {
        file: "e2e/auth-shell.spec.ts",
        title: "unverified members page stays locked > members page redirects",
        outcome: "expected",
        attempts: 1,
      },
    ]);

    expect(report.markdown).toContain("e2e/profile-completion.spec.ts");
    expect(report.markdown).toContain("setup saves profile");
    expect(report.markdown).toContain("| 3 |");
    // The passing test is not listed as a flake.
    expect(report.markdown).not.toContain("auth-shell.spec.ts");
  });

  it("reports an empty run as clean rather than dividing by zero", () => {
    const report = buildFlakeReport([]);

    expect(report.executed).toBe(0);
    expect(report.ratePct).toBe(0);
    expect(report.gateTripped).toBe(false);
  });
});

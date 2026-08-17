// Flake accounting for the Playwright suites, kept separate from the reporter
// that feeds it so the arithmetic and the gate decision are directly testable
// without constructing a Playwright `Suite`.

/** One executed test, reduced to what flake accounting needs. */
export interface FlakeEntry {
  /** Spec path relative to the Playwright rootDir. */
  readonly file: string;
  /** Full test title, root suite down to the test. */
  readonly title: string;
  /** Playwright's own verdict for the test across all its attempts. */
  readonly outcome: "skipped" | "expected" | "unexpected" | "flaky";
  /** How many attempts ran. A flaky test has at least two. */
  readonly attempts: number;
}

export interface FlakeReport {
  readonly executed: number;
  readonly flaky: number;
  readonly failed: number;
  /** Flaky tests over executed tests, in percent, rounded to one decimal. */
  readonly ratePct: number;
  /** True when the run must be failed for flakiness alone. */
  readonly gateTripped: boolean;
  readonly markdown: string;
}

/**
 * Ceiling on retry-passes for a run to count as clean.
 *
 * Zero, deliberately. `Web E2E (full stack)` is being promoted to a required
 * merge check, and a retry-pass is precisely the failure mode that promotion
 * has to survive: it blocks main at random and teaches everyone to press
 * re-run until green, which is the same habit whether the flake rate is 8
 * percent or 4. Any non-zero percentage also silently loosens as the suite
 * grows, since the same one flaky test scores better in a bigger denominator.
 *
 * Retries stay switched on. They are what produces the second attempt, the
 * trace and the video that identify the flake. What changes is that a run
 * which needed them no longer reports success.
 *
 * Raising this is a one line diff a reviewer can see and argue with, which is
 * the point. There is no environment variable for it: a threshold that can be
 * relaxed from outside the repository is a threshold nobody can audit.
 */
export const MAX_FLAKY_TESTS = 0;

function pct(part: number, whole: number): number {
  if (whole === 0) return 0;
  return Math.round((part / whole) * 1000) / 10;
}

export function buildFlakeReport(entries: readonly FlakeEntry[]): FlakeReport {
  const executedEntries = entries.filter((e) => e.outcome !== "skipped");
  const flakyEntries = entries.filter((e) => e.outcome === "flaky");
  const failed = entries.filter((e) => e.outcome === "unexpected").length;
  const executed = executedEntries.length;
  const flaky = flakyEntries.length;
  const ratePct = pct(flaky, executed);
  const gateTripped = flaky > MAX_FLAKY_TESTS;

  const lines: string[] = [];
  lines.push("### Playwright flake report");
  lines.push("");
  lines.push(
    `${executed} executed, ${flaky} passed only on retry, ${failed} failed. Flake rate ${ratePct}% (gate: at most ${MAX_FLAKY_TESTS} retry-pass).`
  );
  lines.push("");

  if (flaky > 0) {
    lines.push("| Spec | Test | Attempts |");
    lines.push("| --- | --- | --- |");
    for (const e of flakyEntries) {
      lines.push(`| \`${e.file}\` | ${e.title} | ${e.attempts} |`);
    }
    lines.push("");
  }

  if (gateTripped) {
    lines.push(
      `**Failing this run for flakiness.** A test that only passes on a retry is not a passing test; it is a random future block on main. Fix the cause, do not raise the retry count. Every attempt's trace and video is in the \`playwright-report\` artifact.`
    );
  } else if (flaky === 0) {
    lines.push("No test needed a retry.");
  }

  return {
    executed,
    flaky,
    failed,
    ratePct,
    gateTripped,
    markdown: lines.join("\n") + "\n",
  };
}

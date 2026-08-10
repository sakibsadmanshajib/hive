import { appendFileSync } from "node:fs";
import type {
  FullConfig,
  FullResult,
  Reporter,
  Suite,
  TestCase,
} from "@playwright/test/reporter";
import { buildFlakeReport, type FlakeEntry } from "./flake-report";

/**
 * Surfaces retry-passes and fails the run on them.
 *
 * The `list` reporter's tail line counts passes and failures. A test that
 * failed twice and passed on its third attempt is counted in the passes, so
 * `24 passed` reads identically whether every test passed first try or two of
 * them only got there on a retry. That is the whole point of a required
 * check being flaky, and it was invisible in the job log: finding the two
 * retry-passes behind CI run 31361681115 meant downloading a 5MB artifact and
 * unzipping the HTML report's embedded JSON by hand.
 *
 * This writes the list of retry-passes into the GitHub job summary, prints it
 * to stdout for local runs, and fails the run when there is one at all
 * (see `MAX_FLAKY_TESTS`).
 */
export default class FlakeReporter implements Reporter {
  private root: Suite | undefined;

  onBegin(_config: FullConfig, suite: Suite): void {
    this.root = suite;
  }

  async onEnd(
    result: FullResult
  ): Promise<{ status?: FullResult["status"] } | undefined> {
    const tests: TestCase[] = this.root ? this.root.allTests() : [];
    const entries: FlakeEntry[] = tests.map((test) => ({
      file: test.location.file.split("/").slice(-2).join("/"),
      // titlePath() is ["", project, file, ...describes, title]. Filter the
      // empty root out first, then drop project and file: the file has its own
      // column and the project is constant for this config. Slicing before
      // filtering keeps the file and repeats it in both columns.
      title: test.titlePath().filter(Boolean).slice(2).join(" > "),
      outcome: test.outcome(),
      attempts: test.results.length,
    }));

    const report = buildFlakeReport(entries);
    process.stdout.write("\n" + report.markdown);

    const summaryPath = process.env.GITHUB_STEP_SUMMARY;
    if (summaryPath) {
      appendFileSync(summaryPath, report.markdown);
    }

    if (report.gateTripped && result.status === "passed") {
      return { status: "failed" };
    }
    return undefined;
  }
}

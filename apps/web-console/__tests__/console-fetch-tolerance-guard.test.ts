// @vitest-environment node
//
// A filesystem scan, no DOM.
import { readFileSync, readdirSync, statSync } from "node:fs";
import { join, relative } from "node:path";

import { describe, expect, it } from "vitest";

/**
 * Issue #494: several console Server Components called a control-plane read
 * with no tolerance, so one non-2xx answer threw out of the component and
 * tore down the whole page tree. The fix routes those reads through
 * lib/console/data.ts. This guard is what makes a call site added next week
 * inherit that instead of quietly reintroducing the crash: patching only the
 * three files the issue named would have left the other nineteen broken, and
 * nothing would have said so.
 */

const CONSOLE_DIR = join(__dirname, "..", "app", "console");
const APP_DIR = join(__dirname, "..", "app");

/** The control-plane reads a console Server Component can perform. */
const READS = [
  "getViewer",
  "getBalance",
  "getAccountProfile",
  "getBillingProfile",
  "getFeatureGates",
  "getMarketplaceEntries",
  "getProviders",
  "getLedgerEntries",
  "getInvoices",
  "getCheckoutRails",
  "getCheckoutIntent",
  "getApiKeys",
  "getApiKeyLimits",
  "getCatalogModels",
  "getAnalyticsUsage",
  "getAnalyticsSpend",
  "getAnalyticsErrors",
  "getUsageEvents",
  "getBudgetThreshold",
  "getBudget",
  "getMembers",
  "listSpendAlerts",
  "listWorkspaceInvoices",
  "getWorkspaceInvoice",
] as const;

/**
 * The two reads every console page performs. They are answered once per
 * request by lib/console/data.ts, which holds the single correct failure
 * behaviour for each: sign-in for a viewer that cannot be resolved, an
 * explicit unknown for a profile that cannot be read.
 */
const SHARED_READS = ["getViewer", "getAccountProfile"] as const;

/** Idioms that make a read's failure survivable at the call site. */
const TOLERANCE = [".catch(", "tolerate(", "Promise.allSettled("];

function tsxFilesUnder(dir: string): string[] {
  const out: string[] = [];
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry);
    if (statSync(full).isDirectory()) {
      out.push(...tsxFilesUnder(full));
    } else if (entry.endsWith(".tsx")) {
      out.push(full);
    }
  }
  return out;
}

/** Line ranges spanned by a `try {` block, by brace depth. */
function tryRanges(lines: string[]): Array<[number, number]> {
  const ranges: Array<[number, number]> = [];
  lines.forEach((line, index) => {
    if (!/\btry\s*\{/.test(line)) {
      return;
    }
    let depth = 0;
    for (let cursor = index; cursor < lines.length; cursor++) {
      depth +=
        (lines[cursor].match(/\{/g) ?? []).length -
        (lines[cursor].match(/\}/g) ?? []).length;
      if (depth <= 0) {
        ranges.push([index, cursor]);
        return;
      }
    }
  });
  return ranges;
}

const consoleFiles = tsxFilesUnder(CONSOLE_DIR);

describe("console server components tolerate a failing control-plane read", () => {
  it("finds the console pages to check", () => {
    expect(consoleFiles.length).toBeGreaterThan(15);
  });

  it("routes the viewer and account-profile reads through lib/console/data", () => {
    const offenders: string[] = [];

    for (const file of consoleFiles) {
      const source = readFileSync(file, "utf8");
      const clientImport = source.match(
        /import\s*\{([^}]*)\}\s*from\s*"@\/lib\/control-plane\/client"/s,
      );
      if (!clientImport) {
        continue;
      }
      for (const read of SHARED_READS) {
        if (new RegExp(`\\b${read}\\b`).test(clientImport[1])) {
          offenders.push(`${relative(APP_DIR, file)} imports ${read} directly`);
        }
      }
    }

    expect(offenders).toEqual([]);
  });

  it("leaves no control-plane read able to throw out of a page", () => {
    const offenders: string[] = [];
    const callPattern = new RegExp(`\\b(${READS.join("|")})\\s*\\(`);

    for (const file of consoleFiles) {
      const lines = readFileSync(file, "utf8").split("\n");
      const guarded = tryRanges(lines);

      lines.forEach((line, index) => {
        const trimmed = line.trim();
        if (
          trimmed.startsWith("//") ||
          trimmed.startsWith("*") ||
          trimmed.startsWith("import")
        ) {
          return;
        }
        if (!callPattern.test(line)) {
          return;
        }
        if (guarded.some(([start, end]) => index >= start && index <= end)) {
          return;
        }
        // A read and its tolerance can straddle a few lines: tolerate(...)
        // may open on the line above and the argument list may run several
        // below, so look at the statement rather than the line.
        const statement = lines
          .slice(Math.max(0, index - 1), index + 4)
          .join("\n");
        if (TOLERANCE.some((idiom) => statement.includes(idiom))) {
          return;
        }
        offenders.push(`${relative(APP_DIR, file)}:${index + 1}: ${trimmed}`);
      });
    }

    expect(offenders).toEqual([]);
  });
});

#!/usr/bin/env node
// Structural guard for issue #1694 (owner ruling, .wolf/decisions.md D-070).
//
// Balance, usage and spend surfaces render Hive credits and no currency at
// all. Currency appears on an invoice, which is a record of a payment made in
// that currency, and on the purchase flow, which quotes a price the customer
// is asked to pay. The ruling is a confidentiality boundary rather than a
// style preference: credits are sold at a markup and a subscription grants a
// credit quantity whose internal value is unpublished, so a balance rendered
// in a currency beside a price paid in that currency hands the customer the
// peg.
//
// The component tests that landed with the fix assert no currency in the
// rendered output of the five components that carried the leak. That protects
// five files. This protects the codebase: the two currency formatters and the
// `Intl` currency style stay importable and constructible anywhere, and the
// sixth surface, written next month, has no test of its own.
//
// What is enforced:
//
//   1. `style: "currency"` may be constructed in exactly one module,
//      lib/format/money.ts, which is the currency formatter itself.
//   2. `formatCurrency` and `formatTakaSubunits` may be named only by that
//      module, by the three surfaces the ruling exempts, and by tests.
//   3. The Hive authored half of the chat front end may not name a currency
//      formatter or construct a currency style at all: it has no invoice and
//      no purchase flow, so it has no exempt surface.
//
// A new exempt surface is a product decision, and adding a path here is how it
// gets recorded. Mirrors the pattern of the other tools/lint-*.mjs source
// scanners wired into the repo-policy-lints CI job.

import { readFileSync, readdirSync, statSync } from "node:fs";
import { dirname, join, relative, resolve, sep } from "node:path";
import { fileURLToPath } from "node:url";

const HERE = dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = resolve(HERE, "..");

const ROOTS = ["apps/web-console", "vendor/open-webui/src/lib/hive"];
const EXTENSIONS = [".ts", ".tsx", ".svelte"];
const SKIP_DIRS = new Set(["node_modules", ".next", "dist", "build", ".svelte-kit"]);

// The currency formatter itself, plus the three surfaces the ruling exempts.
// Anything else naming a currency formatter is a surface that must not.
export const ALLOWED = new Set([
  "apps/web-console/lib/format/money.ts",
  "apps/web-console/components/billing/invoice-list.tsx",
  "apps/web-console/components/billing/invoice-download-button.tsx",
  "apps/web-console/components/billing/checkout-modal.tsx",
]);

const CURRENCY_STYLE = /style\s*:\s*["']currency["']/;
const CURRENCY_FORMATTER = /\b(formatCurrency|formatTakaSubunits)\b/;

// A test naming a formatter is asserting on it, which is the opposite of
// rendering one. The guards this lint stands beside are themselves such files.
const isTest = (rel) => /\.(test|spec)\.(ts|tsx)$/.test(rel);
const isChat = (rel) => rel.startsWith("vendor/open-webui/");

export function checkFile(rel, src) {
  const offences = [];
  // Comments are stripped before matching so that a file explaining why it
  // does NOT use a currency formatter does not trip on its own explanation.
  const code = src
    .replace(/\/\*[\s\S]*?\*\//g, "")
    .split("\n")
    .map((line) => line.replace(/\/\/.*$/, ""))
    .join("\n");

  const named = CURRENCY_FORMATTER.test(code);
  const styled = CURRENCY_STYLE.test(code);
  if (!named && !styled) return offences;

  if (isChat(rel)) {
    // No exemption and no test carve-out: the chat front end has no invoice
    // and no purchase flow, so a currency there is a leak even in a fixture.
    // Its guards assert against a pattern held in currency-mark.ts, which
    // names no formatter and constructs no style.
    if (named) offences.push(`${rel}: chat front end may not name a currency formatter`);
    if (styled) offences.push(`${rel}: chat front end may not construct a currency style`);
    return offences;
  }

  if (styled && rel !== "apps/web-console/lib/format/money.ts") {
    offences.push(
      `${rel}: constructs Intl \`style: "currency"\` outside lib/format/money.ts`,
    );
  }
  if (named && !ALLOWED.has(rel) && !isTest(rel)) {
    offences.push(
      `${rel}: names a currency formatter, and is not an invoice or purchase surface`,
    );
  }
  return offences;
}

function* walk(dir) {
  for (const entry of readdirSync(dir)) {
    if (SKIP_DIRS.has(entry)) continue;
    const full = join(dir, entry);
    if (statSync(full).isDirectory()) {
      yield* walk(full);
      continue;
    }
    if (EXTENSIONS.some((ext) => entry.endsWith(ext))) yield full;
  }
}

function selfTest() {
  const cases = [
    ["apps/web-console/components/billing/credit-balance.tsx", 'formatCurrency(x, "USD")', 1],
    ["apps/web-console/components/billing/invoice-list.tsx", 'formatCurrency(x, "USD")', 0],
    ["apps/web-console/lib/format/money.ts", 'style: "currency",', 0],
    ["apps/web-console/lib/format/credits.ts", 'style: "currency",', 1],
    ["apps/web-console/lib/format/format.test.ts", "formatCurrency(1, 'USD')", 0],
    ["vendor/open-webui/src/lib/hive/credits.ts", "formatCurrency(1)", 1],
    ["vendor/open-webui/src/lib/hive/credits.test.ts", "style: 'currency'", 1],
    ["apps/web-console/components/billing/credit-balance.tsx", "// formatCurrency is deliberately absent", 0],
    ["apps/web-console/components/billing/credit-balance.tsx", "const a = 1;", 0],
  ];
  let asserted = 0;
  for (const [rel, src, expected] of cases) {
    const got = checkFile(rel, src).length;
    if ((got > 0 ? 1 : 0) !== expected) {
      console.error(
        `lint-no-currency-on-credit-surfaces: self-test failed for ${rel} (${src}): expected ${expected}, got ${got}`,
      );
      process.exit(2);
    }
    asserted += 1;
  }
  console.log(`lint-no-currency-on-credit-surfaces: self-test ok (${asserted} cases)`);
}

function main() {
  selfTest();
  const offences = [];
  let scanned = 0;
  for (const root of ROOTS) {
    const abs = join(REPO_ROOT, root);
    for (const file of walk(abs)) {
      const rel = relative(REPO_ROOT, file).split(sep).join("/");
      scanned += 1;
      offences.push(...checkFile(rel, readFileSync(file, "utf8")));
    }
  }
  // Anti vacuity: a walk that silently found nothing would pass. The exempt
  // surfaces are known to exist, so the scan has to have reached them.
  const missed = [...ALLOWED].filter((rel) => {
    try {
      statSync(join(REPO_ROOT, rel));
      return false;
    } catch {
      return true;
    }
  });
  if (missed.length > 0) {
    console.error(
      `lint-no-currency-on-credit-surfaces: allowlisted path no longer exists: ${missed.join(", ")}`,
    );
    process.exit(2);
  }
  if (offences.length > 0) {
    console.error("currency on a credit surface:\n");
    for (const offence of offences) console.error(`  - ${offence}`);
    console.error(
      "\nBalance, usage and spend surfaces render Hive credits and no currency" +
        "\n(owner ruling, .wolf/decisions.md D-070, issue #1694). If a new surface" +
        "\nis genuinely an invoice or a purchase flow, add it to ALLOWED in this" +
        "\nfile, which is how that decision gets recorded.",
    );
    process.exit(1);
  }
  console.log(
    `lint-no-currency-on-credit-surfaces: ok (${scanned} files scanned, ${ALLOWED.size} exempt surfaces)`,
  );
}

main();

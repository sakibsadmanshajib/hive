// Phase 14 customer-USD lint primitive — RED-first node:test suite.
// Runs synthetic specs in os.tmpdir(); spawns the lint script as a child
// process so we exercise the real CLI surface end-to-end.

import { test } from "node:test";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdtempSync, writeFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const HERE = dirname(fileURLToPath(import.meta.url));
const SCRIPT = resolve(HERE, "lint-no-customer-usd.mjs");

function runLint(args) {
  return spawnSync(process.execPath, [SCRIPT, ...args], { encoding: "utf8" });
}

function withTempSpec(name, body, fn) {
  const dir = mkdtempSync(join(tmpdir(), "lint-fx-"));
  try {
    const file = join(dir, name);
    writeFileSync(file, body, "utf8");
    return fn(file);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
}

test("clean BDT-only spec exits 0", () => {
  withTempSpec("clean.yaml", `
openapi: 3.0.0
info:
  title: Clean
  version: 1.0.0
paths:
  /things:
    get:
      responses:
        "200":
          description: ok
components:
  schemas:
    Thing:
      type: object
      properties:
        amount_bdt_subunits:
          type: string
        currency:
          type: string
          enum: [BDT]
`, (file) => {
    const r = runLint([file]);
    assert.equal(r.status, 0, `expected exit 0, got ${r.status}; stderr=${r.stderr}`);
  });
});

test("amount_usd key is flagged", () => {
  withTempSpec("dirty.yaml", `
components:
  schemas:
    Invoice:
      type: object
      properties:
        amount_usd:
          type: string
`, (file) => {
    const r = runLint([file]);
    assert.equal(r.status, 1);
    assert.match(r.stderr, /amount_usd/);
  });
});

test("usd_-prefixed key is flagged", () => {
  withTempSpec("dirty2.yaml", `
components:
  schemas:
    Wallet:
      type: object
      properties:
        usd_balance:
          type: string
`, (file) => {
    const r = runLint([file]);
    assert.equal(r.status, 1);
    assert.match(r.stderr, /usd_balance/);
  });
});

test("price_per_credit_usd key is flagged", () => {
  withTempSpec("dirty3.yaml", `
components:
  schemas:
    Pricing:
      type: object
      properties:
        price_per_credit_usd:
          type: string
`, (file) => {
    const r = runLint([file]);
    assert.equal(r.status, 1);
    assert.match(r.stderr, /price_per_credit_usd/);
  });
});

test("exchange_rate key is flagged", () => {
  withTempSpec("dirty4.yaml", `
components:
  schemas:
    Quote:
      type: object
      properties:
        exchange_rate:
          type: string
`, (file) => {
    const r = runLint([file]);
    assert.equal(r.status, 1);
    assert.match(r.stderr, /exchange_rate/);
  });
});

test("fx_-prefixed key is flagged", () => {
  withTempSpec("dirty5.yaml", `
components:
  schemas:
    Quote:
      type: object
      properties:
        fx_rate_basis:
          type: string
`, (file) => {
    const r = runLint([file]);
    assert.equal(r.status, 1);
    assert.match(r.stderr, /fx_rate_basis/);
  });
});

test("USD inside description prose does not trip the lint", () => {
  withTempSpec("prose.yaml", `
components:
  schemas:
    Invoice:
      type: object
      properties:
        amount_bdt_subunits:
          type: string
          description: "Reference: legacy fields like amount_usd were removed in Phase 14."
`, (file) => {
    const r = runLint([file]);
    assert.equal(r.status, 0, `prose mention should not fail; stderr=${r.stderr}`);
  });
});

test("default Phase 14 path files lint clean", () => {
  const r = runLint([]);
  assert.equal(r.status, 0, `expected default Phase 14 paths to be clean; stderr=${r.stderr}`);
});

// ─── Phase 17 (FX-17-06) — Go + TS + whitelist coverage ──────────────────────

function withTempFile(name, body, fn) {
  const dir = mkdtempSync(join(tmpdir(), "lint-fx-"));
  try {
    const file = join(dir, name);
    writeFileSync(file, body, "utf8");
    return fn(file);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
}

test("Go: struct field tag json:\"amount_usd\" is flagged", () => {
  withTempFile("dto.go", `package payments
type Invoice struct {
\tAmountUSD string \`json:"amount_usd"\`
}
`, (file) => {
    const r = runLint([file]);
    assert.equal(r.status, 1, `expected Go LEAK to fail; stderr=${r.stderr}`);
    assert.match(r.stderr, /amount_usd/);
  });
});

test("Go: json:\"-\" tag (internal-only) is clean", () => {
  withTempFile("internal.go", `package payments
type Invoice struct {
\tAmountUSD string \`json:"-"\`
\tAmountBDTSubunits string \`json:"amount_bdt_subunits"\`
}
`, (file) => {
    const r = runLint([file]);
    assert.equal(r.status, 0, `expected Go internal-only to pass; stderr=${r.stderr}`);
  });
});

test("Go: usd_-prefixed json tag is flagged", () => {
  withTempFile("wallet.go", `package wallet
type Wallet struct {
\tUSDBalance string \`json:"usd_balance"\`
}
`, (file) => {
    const r = runLint([file]);
    assert.equal(r.status, 1);
    assert.match(r.stderr, /usd_balance/);
  });
});

test("Go: fx_-prefixed json tag is flagged", () => {
  withTempFile("quote.go", `package quote
type Quote struct {
\tFXRateBasis string \`json:"fx_rate_basis"\`
}
`, (file) => {
    const r = runLint([file]);
    assert.equal(r.status, 1);
    assert.match(r.stderr, /fx_rate_basis/);
  });
});

test("Go: PHASE-17-INTERNAL-ONLY whitelist comment exempts the line", () => {
  withTempFile("stripe.go", `package payments
type StripeIntent struct {
\tAmountUSD string \`json:"amount_usd"\` // PHASE-17-INTERNAL-ONLY: server→Stripe payload, never customer-facing
}
`, (file) => {
    const r = runLint([file]);
    assert.equal(r.status, 0, `expected whitelist to pass; stderr=${r.stderr}`);
  });
});

test("TS: interface field price_per_credit_usd is flagged", () => {
  withTempFile("pricing.ts", `export interface Pricing {
  price_per_credit_usd: number;
  amount_bdt_subunits: string;
}
`, (file) => {
    const r = runLint([file]);
    assert.equal(r.status, 1, `expected TS LEAK to fail; stderr=${r.stderr}`);
    assert.match(r.stderr, /price_per_credit_usd/);
  });
});

test("TS: clean BDT-only interface is clean", () => {
  withTempFile("clean.ts", `export interface Wallet {
  amount_bdt_subunits: string;
  currency: "BDT";
}
`, (file) => {
    const r = runLint([file]);
    assert.equal(r.status, 0, `expected TS clean to pass; stderr=${r.stderr}`);
  });
});

test("TS: optional amount_usd? field is flagged", () => {
  withTempFile("opt.tsx", `export interface Receipt {
  amount_usd?: string;
}
`, (file) => {
    const r = runLint([file]);
    assert.equal(r.status, 1);
    assert.match(r.stderr, /amount_usd/);
  });
});

test("TS: PHASE-17-INTERNAL-ONLY whitelist comment exempts the line", () => {
  withTempFile("internal.ts", `export interface StripeIntent {
  amount_usd: number; // PHASE-17-INTERNAL-ONLY: server-side Stripe call, not exposed
}
`, (file) => {
    const r = runLint([file]);
    assert.equal(r.status, 0, `expected TS whitelist to pass; stderr=${r.stderr}`);
  });
});

test("TS: prose mention of amount_usd in a comment does not trip the lint", () => {
  withTempFile("prose.ts", `// Note: legacy amount_usd was removed in Phase 14.
export interface Wallet {
  amount_bdt_subunits: string;
}
`, (file) => {
    const r = runLint([file]);
    assert.equal(r.status, 0, `expected TS prose to pass; stderr=${r.stderr}`);
  });
});

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

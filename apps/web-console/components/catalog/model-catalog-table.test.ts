import { existsSync, readdirSync, readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

import { describe, expect, it } from "vitest";

import { statusBadge } from "./model-catalog-table";

// The lifecycle enum is owned by the database: public.model_aliases carries
// `check (lifecycle in ('stable', 'preview', 'hidden'))`
// (supabase/migrations/20260331_01_model_catalog.sql). Every seeded alias is
// 'stable'. The console previously mapped only 'active', a value the enum
// has never contained, so every shipping model rendered as "Unavailable".
const MIGRATIONS_DIR = join(
  dirname(fileURLToPath(import.meta.url)),
  "..",
  "..",
  "..",
  "..",
  "supabase",
  "migrations",
);

// Reads the lifecycle values the database actually allows straight out of the
// migrations, so adding one to the constraint fails this suite until
// statusBadge learns it. A hardcoded list could not do that: it would let a new
// enum value render as "Unavailable" for as long as nobody remembered to edit
// the test, which is how the 'stable' bug reached production in the first place.
//
// Union across every migration rather than only the newest match. A value a
// later migration drops leaves statusBadge mapping a label nothing emits, which
// is harmless; a value any migration adds is what has to be caught.
function lifecycleValuesAllowedByTheDatabase(): string[] {
  if (!existsSync(MIGRATIONS_DIR)) {
    return [];
  }
  const constraint = /check\s*\(\s*lifecycle\s+in\s*\(([^)]*)\)/gi;
  const values = new Set<string>();
  for (const file of readdirSync(MIGRATIONS_DIR)) {
    if (!file.endsWith(".sql")) {
      continue;
    }
    for (const match of readFileSync(join(MIGRATIONS_DIR, file), "utf8").matchAll(
      constraint,
    )) {
      for (const raw of match[1].split(",")) {
        const value = raw.trim().replace(/^'/, "").replace(/'$/, "");
        if (value) {
          values.add(value);
        }
      }
    }
  }
  return [...values];
}

const constraintValues = lifecycleValuesAllowedByTheDatabase();

describe("statusBadge", () => {
  it("maps the real database lifecycle values", () => {
    expect(statusBadge("stable")).toEqual({
      label: "Available",
      tone: "success",
    });
    expect(statusBadge("preview")).toEqual({
      label: "Preview",
      tone: "warning",
    });
    expect(statusBadge("hidden")).toEqual({
      label: "Hidden",
      tone: "neutral",
    });
  });

  it("still maps the legacy 'active' fallback emitted by the client parser", () => {
    // lib/control-plane/client.ts defaults a missing lifecycle field to
    // "active". Until that default is corrected, dropping this mapping would
    // reintroduce the bug for any alias whose lifecycle field is absent.
    expect(statusBadge("active")).toEqual({
      label: "Available",
      tone: "success",
    });
  });

  it("falls back to Unavailable for a lifecycle value it does not know", () => {
    expect(statusBadge("deprecated")).toEqual({
      label: "Unavailable",
      tone: "neutral",
    });
    expect(statusBadge("")).toEqual({ label: "Unavailable", tone: "neutral" });
  });

  // Skipped only where supabase/migrations is not on disk, which is the
  // deploy/docker `web-console` image (Dockerfile.web-console copies
  // apps/web-console alone). CI and any repo-root run have the migrations and
  // enforce this.
  it.skipIf(constraintValues.length === 0)(
    "covers every lifecycle value the database check constraint allows",
    () => {
      for (const lifecycle of constraintValues) {
        expect(
          statusBadge(lifecycle).label,
          `lifecycle '${lifecycle}' is allowed by the model_aliases check constraint but has no badge`,
        ).not.toBe("Unavailable");
      }
    },
  );
});

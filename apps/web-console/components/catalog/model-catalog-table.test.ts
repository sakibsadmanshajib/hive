import { describe, expect, it } from "vitest";

import { statusBadge } from "./model-catalog-table";

// The lifecycle enum is owned by the database: public.model_aliases carries
// `check (lifecycle in ('stable', 'preview', 'hidden'))`
// (supabase/migrations/20260331_01_model_catalog.sql). Every seeded alias is
// 'stable'. The console previously mapped only 'active', a value the enum
// has never contained, so every shipping model rendered as "Unavailable".
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

  it("covers every value the database check constraint allows", () => {
    // Guard against a future enum value silently rendering as Unavailable:
    // extend both this list and statusBadge together when the constraint
    // changes.
    const allowedByConstraint = ["stable", "preview", "hidden"];
    for (const lifecycle of allowedByConstraint) {
      expect(statusBadge(lifecycle).label).not.toBe("Unavailable");
    }
  });
});

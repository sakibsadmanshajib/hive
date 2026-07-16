import { describe, expect, it } from "vitest";

import {
  buildCreateRequest,
  formatStatus,
  isLocalTaskView,
  packLabel,
  PACKS,
} from "./local-tasks";

describe("buildCreateRequest", () => {
  it("trims instructions", () => {
    expect(buildCreateRequest("coding-pack", "  do it  ")).toEqual({
      pack: "coding-pack",
      instructions: "do it",
    });
  });

  it("allows empty instructions", () => {
    expect(buildCreateRequest("knowledge-work-pack", "")).toEqual({
      pack: "knowledge-work-pack",
      instructions: "",
    });
  });
});

describe("formatStatus", () => {
  it("labels running", () => {
    expect(formatStatus("running")).toBe("Running");
  });

  it("labels failed", () => {
    expect(formatStatus("failed")).toBe("Failed");
  });
});

describe("packLabel", () => {
  it("resolves a known pack to its label", () => {
    expect(packLabel("coding-pack")).toBe("Coding pack");
  });

  it("falls back to the raw value for an unknown pack", () => {
    expect(packLabel("mystery-pack")).toBe("mystery-pack");
  });

  it("covers every declared pack option", () => {
    for (const { value, label } of PACKS) {
      expect(packLabel(value)).toBe(label);
    }
  });
});

describe("isLocalTaskView", () => {
  const valid = {
    id: "local-1-1",
    pack: "coding-pack",
    instructions: "",
    status: "running",
    created_at: "1700000000",
  };

  it("accepts a well-formed task", () => {
    expect(isLocalTaskView(valid)).toBe(true);
  });

  it("rejects null", () => {
    expect(isLocalTaskView(null)).toBe(false);
  });

  it("rejects a non-object", () => {
    expect(isLocalTaskView("nope")).toBe(false);
  });

  it("rejects a missing field", () => {
    const { id: _id, ...rest } = valid;
    expect(isLocalTaskView(rest)).toBe(false);
  });

  it("rejects an invalid status value", () => {
    expect(isLocalTaskView({ ...valid, status: "queued" })).toBe(false);
  });

  it("rejects a wrong-typed field", () => {
    expect(isLocalTaskView({ ...valid, instructions: 42 })).toBe(false);
  });
});

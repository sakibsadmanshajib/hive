// Unit cover for the chat-coverage gate's pure logic.
//
// These run in the required "Web console" CI job, with no browser and no
// deployment, and they exist because the parts of the gate most able to lie
// are the parts that never touch a page: how a ratio is computed, when a floor
// fails, and what gets redacted before it is written down. The identity figure
// this suite reports was once computed after the fact by hand, from a ledger,
// with nothing exercising the function that is supposed to produce it.
import { readFileSync } from "node:fs";
import { join } from "node:path";

import { describe, expect, it } from "vitest";

import {
  floorFailures,
  identityOf,
  isDestructive,
  parseFloors,
  parseExclusions,
  redactUrl,
  requestSignature,
  summarise,
  type Control,
  type Result,
} from "../../e2e/chat-coverage/lib";

const COVERAGE_DIR = join(__dirname, "../../e2e/chat-coverage");

function result(key: string, over: Partial<Result> = {}): Result {
  return {
    key,
    surface: key.split("::")[0],
    name: "control",
    role: "button",
    proven: true,
    proof: "dom",
    detail: "",
    ...over,
  };
}

function control(over: Partial<Control> = {}): Control {
  return {
    key: "home::button::Thing",
    surface: "home",
    covId: "home#1",
    tag: "button",
    type: "",
    role: "button",
    name: "Thing",
    label: "Thing",
    disabled: false,
    reason: "",
    state: null,
    href: "",
    contentEditable: false,
    ...over,
  };
}

describe("summarise", () => {
  it("counts one identity per control, however many rows render it", () => {
    const summary = summarise([
      result("sidebar::button::Chat Menu"),
      result("sidebar::button::Chat Menu#2"),
      result("sidebar::button::Chat Menu#3"),
      result("home::button::New Chat"),
    ]);
    expect(summary.identities).toBe(2);
    expect(summary.identitiesProven).toBe(2);
    expect(summary.identityRatio).toBe(1);
    // The instance figure still moves with the account's data. That is why it
    // is the secondary number.
    expect(summary.total).toBe(4);
  });

  it("does not prove an identity when one of its instances failed", () => {
    const summary = summarise([
      result("sidebar::button::Chat Menu"),
      result("sidebar::button::Chat Menu#2", { proven: false, proof: "none", detail: "no effect" }),
    ]);
    expect(summary.identities).toBe(1);
    expect(summary.identitiesProven).toBe(0);
  });

  it("keeps a control that was never fired out of both proven and failing", () => {
    const summary = summarise([
      result("home::button::New Chat"),
      result("settings::button::Delete All Chats", {
        proven: false,
        proof: "not-fired",
        detail: "destructive by label",
      }),
    ]);
    expect(summary.proven).toBe(1);
    expect(summary.deferred).toBe(1);
    expect(summary.identitiesProven).toBe(1);
    expect(summary.identitiesDeferred).toBe(1);
    expect(summary.surfaces.settings.deferred).toBe(1);
    // A not-fired control is not an unproven one either: it never reaches the
    // failure list the gate asserts on.
    expect(summary.surfaces.settings.unproven).toEqual([]);
  });

  it("collapses only the ordinal suffix", () => {
    expect(identityOf("search::button::Chat Menu#52")).toBe("search::button::Chat Menu");
    expect(identityOf("search::button::GPT-4#1 turbo")).toBe("search::button::GPT-4#1 turbo");
  });
});

describe("floorFailures", () => {
  const floors = { home: 28, "settings:Interface": 56 };

  it("fails a surface that renders less than it used to", () => {
    const failures = floorFailures(floors, [
      { surface: "home", enumerated: 28 },
      { surface: "settings:Interface", enumerated: 48 },
    ]);
    expect(failures).toHaveLength(1);
    expect(failures[0]).toContain("settings:Interface");
    expect(failures[0]).toContain("below its recorded floor of 56");
  });

  it("fails a floor key the run never swept at all", () => {
    // The defect this replaced: the guard iterated the swept list, so a
    // surface that stopped opening took its own floor with it and the
    // denominator shrank with nothing said.
    const failures = floorFailures(floors, [{ surface: "home", enumerated: 28 }]);
    expect(failures).toHaveLength(1);
    expect(failures[0]).toContain("settings:Interface");
    expect(failures[0]).toContain("never swept it");
  });

  it("does not fail a floor key outside the scope of a sliced run", () => {
    const failures = floorFailures(
      floors,
      [{ surface: "home", enumerated: 28 }],
      (surface) => surface === "home",
    );
    expect(failures).toEqual([]);
  });

  it("fails a swept surface that has no floor recorded", () => {
    const failures = floorFailures({}, [{ surface: "brand-new", enumerated: 4 }]);
    expect(failures[0]).toContain("no floor in surface-floors.json");
  });
});

describe("the committed floors", () => {
  const floors = parseFloors(
    JSON.parse(readFileSync(join(COVERAGE_DIR, "surface-floors.json"), "utf8")),
  );
  const recorded = JSON.parse(
    readFileSync(join(COVERAGE_DIR, "results/2026-08-10-morning-live-run.json"), "utf8"),
  );

  // The regression this file exists for. A run once rewrote the floors from
  // itself and skipped checking them in the same pass, so three surfaces that
  // had genuinely degraded (Interface 56 -> 48, General 17 -> 16, Audio 8 -> 7)
  // had the smaller numbers written in as the new baseline. Floors are now
  // updated by a separate program, and this keeps the committed file honest
  // against the highest count any recorded run has ever seen.
  it("is never below what a recorded live run enumerated", () => {
    const below: string[] = [];
    for (const [surface, seen] of Object.entries(recorded.surfaces)) {
      const total = Number(new Map(Object.entries(seen ?? {})).get("total"));
      const floor = floors[surface];
      if (floor === undefined) continue;
      if (floor < total) below.push(`${surface}: floor ${floor} < recorded ${total}`);
    }
    expect(below).toEqual([]);
  });
});

describe("redactUrl", () => {
  it("scrubs an OAuth callback in the query string", () => {
    expect(redactUrl("https://chat.example.test/oauth/callback?code=abc123&state=xyz789")).toBe(
      "https://chat.example.test/oauth/callback?code=REDACTED&state=REDACTED",
    );
  });

  it("scrubs the same credentials out of a fragment", () => {
    const redacted = redactUrl(
      "https://chat.example.test/#access_token=abc123&refresh_token=def456&type=magiclink",
    );
    expect(redacted).not.toContain("abc123");
    expect(redacted).not.toContain("def456");
    expect(redacted).toContain("type=magiclink");
  });

  it("leaves an ordinary URL alone", () => {
    expect(redactUrl("https://chat.example.test/api/v1/chats?page=2")).toBe(
      "https://chat.example.test/api/v1/chats?page=2",
    );
  });
});

describe("requestSignature", () => {
  it("collapses ids and query strings so a poll is recognisable again", () => {
    expect(
      requestSignature("https://x.test/api/v1/chats/9f1c42e0-6b1d-4d5e-9a10-2f1c8ab90001?ts=1"),
    ).toBe(
      requestSignature("https://x.test/api/v1/chats/0a2b77f1-1c2d-4e5f-8a90-3b1c8ab90002?ts=2"),
    );
  });

  it("keeps two different endpoints apart", () => {
    expect(requestSignature("https://x.test/api/v1/chats")).not.toBe(
      requestSignature("https://x.test/api/v1/models"),
    );
  });
});

describe("isDestructive", () => {
  it("matches on the label a user reads", () => {
    expect(isDestructive(control({ label: "Delete All Chats" }))).toBe(true);
    expect(isDestructive(control({ label: "Archive" }))).toBe(true);
    expect(isDestructive(control({ label: "New Chat" }))).toBe(false);
  });
});

describe("the data-file validators", () => {
  it("rejects a floor that is not a positive integer", () => {
    expect(() => parseFloors({ surfaces: { home: "28" } })).toThrow(/positive integer/);
    expect(() => parseFloors({ surfaces: { home: 0 } })).toThrow(/positive integer/);
  });

  it("rejects an exclusions file with no array", () => {
    expect(() => parseExclusions({ surfaces: {} })).toThrow(/expected a JSON array/);
  });

  it("reads the committed exclusions", () => {
    const parsed = parseExclusions(
      JSON.parse(readFileSync(join(COVERAGE_DIR, "surface-exclusions.json"), "utf8")),
    );
    for (const entry of parsed) {
      expect(entry.id).not.toBe("");
    }
  });
});

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
  exclusionFailures,
  parseDataDriven,
  expiredExclusions,
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
import { STATIC_SURFACES } from "../../e2e/chat-coverage/surfaces";

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
    // Counted apart from proven so it can never be read as coverage. It is
    // still a failure at the gate unless inert-registry.json justifies the
    // key, which the spec decides; this column only keeps the two apart in the
    // report.
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

  it("keeps a data-driven surface at a presence bar rather than a pinned count", () => {
    // The chat list enumerated 50 controls one run and 106 the next with no
    // product change: one row per chat. Pinning that count reds the gate when
    // somebody deletes chats. A floor of 1 still catches the surface
    // disappearing, which is the case that matters.
    const dataDriven = parseDataDriven(
      JSON.parse(readFileSync(join(COVERAGE_DIR, "surface-floors.json"), "utf8")),
    );
    const floors = parseFloors(
      JSON.parse(readFileSync(join(COVERAGE_DIR, "surface-floors.json"), "utf8")),
    );
    for (const surface of dataDriven) {
      expect(floors[surface], `${surface} must keep a presence bar`).toBe(1);
    }
    expect(floorFailures(floors, [{ surface: "search", enumerated: 40 }], (s) => s === "search")).toEqual([]);
  });
});

describe("the committed floors", () => {
  const floors = parseFloors(
    JSON.parse(readFileSync(join(COVERAGE_DIR, "surface-floors.json"), "utf8")),
  );

  // EVERY recorded run, not one of them. Comparing against the morning ledger
  // alone left `search` pinned at 50 while the committed floor says 106, so a
  // fifth of the whole denominator could have been signed away and this test
  // would still have passed. Highest wins: a floor is the most a surface has
  // ever been seen to render.
  function highestRecorded(): Map<string, number> {
    const seen = new Map<string, number>();
    const ledgers = [
      JSON.parse(readFileSync(join(COVERAGE_DIR, "results/2026-08-10-morning-live-run.json"), "utf8")),
      JSON.parse(
        readFileSync(
          join(COVERAGE_DIR, "../../../../docs/proof/chat-interaction-coverage-2026-08-10/coverage.run.json"),
          "utf8",
        ),
      ),
    ];
    for (const ledger of ledgers) {
      const surfaces = ledger.summary?.surfaces ?? ledger.surfaces ?? {};
      for (const [surface, counts] of Object.entries(surfaces)) {
        const total = Number(new Map(Object.entries(counts ?? {})).get("total"));
        if (!Number.isFinite(total)) continue;
        seen.set(surface, Math.max(seen.get(surface) ?? 0, total));
      }
    }
    return seen;
  }

  it("is never below what any recorded live run enumerated", () => {
    const dataDriven = parseDataDriven(
      JSON.parse(readFileSync(join(COVERAGE_DIR, "surface-floors.json"), "utf8")),
    );
    const below: string[] = [];
    for (const [surface, total] of highestRecorded()) {
      // A data-driven surface keeps a presence bar of 1 rather than a pinned
      // count, so the recorded totals are deliberately not enforced for it.
      if (dataDriven.has(surface)) continue;
      const floor = floors[surface];
      // A missing key is the failure, not a reason to skip. Deleting a floor
      // line is exactly how a surface leaves the denominator quietly, and
      // `continue` here made that invisible.
      if (floor === undefined) {
        below.push(`${surface}: no floor at all, though a recorded run enumerated ${total}`);
        continue;
      }
      if (floor < total) below.push(`${surface}: floor ${floor} < recorded ${total}`);
    }
    expect(below).toEqual([]);
  });

  it("keeps a floor for every surface that must exist even when it does not sweep", () => {
    // sidebar, chat-item-menu, chat-message-actions and workspace:knowledge
    // enumerate nothing today. Their floors are what turn "the entry point was
    // deleted" into a failure rather than into a green run over a smaller app.
    for (const surface of [
      "sidebar",
      "chat-item-menu",
      "chat-message-actions",
      "workspace:knowledge",
    ]) {
      expect(floors[surface], `${surface} lost its floor`).toBeGreaterThan(0);
    }
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
  it("matches the control's own accessible name", () => {
    expect(isDestructive(control({ name: "Delete All Chats" }))).toBe(true);
    expect(isDestructive(control({ name: "Archive All" }))).toBe(true);
    expect(isDestructive(control({ name: "New Chat" }))).toBe(false);
  });

  it("never matches on text lifted from an ancestor", () => {
    // `label` falls back to up to 140 characters of the surrounding panel, so
    // matching it made every button under a "Delete All Chats" heading
    // destructive: two controls the ledger proves dead would have been hidden
    // behind not-fired, and three genuinely proven ones thrown away.
    const inThePanel = control({
      name: "Import Chats",
      label: "Import Chats Export Chats Archive All Chats Delete All Chats",
    });
    expect(isDestructive(inThePanel)).toBe(false);
  });

  it("does not match a word that merely contains one", () => {
    expect(isDestructive(control({ name: "Undeleted items" }))).toBe(false);
    expect(isDestructive(control({ name: "Archived Chats" }))).toBe(false);
  });
});

describe("exclusion bookkeeping", () => {
  // Asserted against deliberately BROKEN input, not only against the committed
  // file. Every earlier assertion here was "the valid file produces no
  // complaints", which stays green if the function is gutted to return [].
  const valid = { id: "composer-controls", reason: "panel will not open", owner: "@x", issue: 844 };

  it("names every missing field", () => {
    expect(exclusionFailures([{ ...valid, reason: "" }]).join(" ")).toContain("no reason");
    expect(exclusionFailures([{ ...valid, owner: undefined }]).join(" ")).toContain("no owner");
    expect(exclusionFailures([{ ...valid, issue: undefined }]).join(" ")).toContain(
      "names no blocking issue",
    );
    expect(exclusionFailures([{ ...valid, permanent: true }]).join(" ")).toContain(
      "one or the other",
    );
    expect(exclusionFailures([valid])).toEqual([]);
  });

  it("expires an exclusion the day its issue closes", () => {
    expect(expiredExclusions([valid], new Set([844]))).toHaveLength(1);
    expect(expiredExclusions([valid], new Set([844]))[0]).toContain("844");
    expect(expiredExclusions([valid], new Set([999]))).toEqual([]);
    expect(expiredExclusions([{ ...valid, issue: undefined, permanent: true }], new Set([844]))).toEqual(
      [],
    );
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

// The skills library shipped in PR #1388 as a Hive route at /skills with its
// own navigation row. Nothing swept it, so it sat outside the denominator
// entirely: a bundle regression, a new proxy rule, or `workspace.skills`
// flipping back to false would have emptied the surface with every gate still
// reporting green. That is the failure shape this repository keeps paying for,
// a quiet absence where a loud failure belonged, so the surface is swept and
// floored.
//
// A presence bar rather than a pinned count, because the index renders one row
// per skill the account owns, which is account data in exactly the way the
// chat list is. A floor of 1 still fails when the surface stops rendering at
// all, and a floor key a run never sweeps fails too, so deleting the entry
// point is caught rather than quietly shrinking the denominator.
describe("the skills surface is inside the denominator", () => {
  const floorsFile = JSON.parse(
    readFileSync(join(COVERAGE_DIR, "surface-floors.json"), "utf8"),
  );

  it("is swept by the live run", () => {
    const ids = STATIC_SURFACES.map((surface) => surface.id);
    expect(ids, "the /skills route must be swept or it leaves the denominator").toContain(
      "skills",
    );
  });

  it("carries a presence bar, not a pinned count", () => {
    expect(parseDataDriven(floorsFile).has("skills")).toBe(true);
    expect(parseFloors(floorsFile).skills).toBe(1);
  });

  it("fails the gate when the surface stops rendering", () => {
    const floors = parseFloors(floorsFile);
    expect(floorFailures(floors, [], (surface) => surface === "skills")).toHaveLength(1);
    expect(
      floorFailures(floors, [{ surface: "skills", enumerated: 3 }], (s) => s === "skills"),
    ).toEqual([]);
  });
});

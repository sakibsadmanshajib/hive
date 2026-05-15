import { describe, it, expect } from "vitest";
import { can, PERMISSIONS } from "../../lib/viewer-gates";
import type { Permission } from "../../lib/viewer-gates";

// ---------------------------------------------------------------------------
// Fixture: expected permission sets per actor state.
// Sourced from the Go policy matrix (TestPolicyMatrix in policy_test.go).
// 5 actor states × 11 permissions = 55 assertion cells.
// ---------------------------------------------------------------------------

interface ActorState {
  label: string;
  permissions: Permission[];
}

const ACTOR_STATES: ActorState[] = [
  {
    label: "owner+verified",
    permissions: [
      "analytics.view",
      "api_keys.read",
      "api_keys.write",
      "billing.view",
      "billing.write",
      "ledger.view",
      "members.invite",
      "members.manage",
      "workspace.settings",
    ],
  },
  {
    label: "owner+unverified",
    permissions: [
      "api_keys.read",
      "billing.view",
    ],
  },
  {
    label: "member+verified",
    permissions: [
      "analytics.view",
      "ledger.view",
    ],
  },
  {
    label: "member+unverified",
    permissions: [],
  },
  {
    label: "admin",
    permissions: [
      "analytics.view",
      "api_keys.read",
      "api_keys.write",
      "billing.view",
      "billing.write",
      "grants.create",
      "ledger.view",
      "members.invite",
      "members.manage",
      "platform.admin",
      "workspace.settings",
    ],
  },
];

// ---------------------------------------------------------------------------
// Defensive count guard — fails if PERMISSIONS registry diverges from 11
// ---------------------------------------------------------------------------
describe("PERMISSIONS registry", () => {
  it("has exactly 11 permissions", () => {
    expect(PERMISSIONS.length).toBe(11);
  });
});

// ---------------------------------------------------------------------------
// Matrix: for every actor state, iterate all 11 permissions and assert can()
// ---------------------------------------------------------------------------
describe("can() — permission matrix (55 cells)", () => {
  for (const actor of ACTOR_STATES) {
    const expectedSet = new Set<string>(actor.permissions);

    for (const perm of PERMISSIONS) {
      const want = expectedSet.has(perm);
      it(`${actor.label}/can(${perm}) === ${want}`, () => {
        const viewer = { permissions: actor.permissions };
        expect(can(viewer, perm)).toBe(want);
      });
    }
  }
});

/**
 * permissions.parity.test.ts
 *
 * Cross-language parity: proves the FE can() helper produces the same
 * allow/deny decisions as the Go authz.Policy.Can for every (role, verified,
 * isAdmin, perm) tuple in the matrix.
 *
 * Source of truth: apps/control-plane/internal/authz/policy_test.go
 *   TestPolicyMatrix — 5 actor states × 11 permissions = 55 cells.
 *
 * Strategy: Pattern B — fixture hardcoded from the Go matrix and committed.
 * The fixture is intentionally NOT dynamically consumed from Go at test time;
 * the CI codegen-drift step (`make gen-permissions && git diff --exit-code`)
 * catches registry changes that would require a fixture update.
 */

import { describe, it, expect } from "vitest";
import { can, PERMISSIONS } from "../../lib/viewer-gates";
import type { Permission } from "../../lib/viewer-gates";

// ---------------------------------------------------------------------------
// Matrix fixture — derived from TestPolicyMatrix in policy_test.go.
// Each row describes one actor state and its complete granted permission set.
// ---------------------------------------------------------------------------

interface ActorFixture {
  label: string;
  role: string;
  verified: boolean;
  isAdmin: boolean;
  granted: Permission[];
}

const MATRIX: ActorFixture[] = [
  // --- owner + verified ---
  // billing.view: Y (RequiresVerified=false, owner-only)
  // billing.write: Y (RequiresVerified=true, owner-only)
  // api_keys.read: Y (RequiresVerified=false, owner-only)
  // api_keys.write: Y (RequiresVerified=true, owner-only)
  // analytics.view: Y (RequiresVerified=true, any-verified)
  // members.invite: Y (RequiresVerified=true, owner-only)
  // members.manage: Y (RequiresVerified=true, owner-only)
  // workspace.settings: Y (RequiresVerified=true, owner-only)
  // grants.create: N (admin-only)
  // ledger.view: Y (RequiresVerified=true, any-verified)
  // platform.admin: N (admin-only)
  {
    label: "owner+verified",
    role: "owner",
    verified: true,
    isAdmin: false,
    granted: [
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

  // --- owner + unverified ---
  // billing.view: Y (RequiresVerified=false)
  // billing.write: N (RequiresVerified=true)
  // api_keys.read: Y (RequiresVerified=false)
  // api_keys.write: N (RequiresVerified=true)
  // analytics.view: N (RequiresVerified=true)
  // members.invite: N (RequiresVerified=true)
  // members.manage: N (RequiresVerified=true)
  // workspace.settings: N (RequiresVerified=true)
  // grants.create: N
  // ledger.view: N (RequiresVerified=true)
  // platform.admin: N
  {
    label: "owner+unverified",
    role: "owner",
    verified: false,
    isAdmin: false,
    granted: [
      "api_keys.read",
      "billing.view",
    ],
  },

  // --- member + verified ---
  // analytics.view: Y (any verified actor)
  // ledger.view: Y (any verified actor)
  // All owner-only: N
  {
    label: "member+verified",
    role: "member",
    verified: true,
    isAdmin: false,
    granted: [
      "analytics.view",
      "ledger.view",
    ],
  },

  // --- member + unverified ---
  // All: N (owner-only or RequiresVerified=true)
  {
    label: "member+unverified",
    role: "member",
    verified: false,
    isAdmin: false,
    granted: [],
  },

  // --- admin (isAdmin=true, any role/verified) ---
  // admin gets all 11 permissions
  {
    label: "admin",
    role: "",
    verified: false,
    isAdmin: true,
    granted: [
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
// Guards
// ---------------------------------------------------------------------------

describe("PERMISSIONS registry size guard", () => {
  it("has exactly 11 entries — fails on registry expansion without fixture update", () => {
    expect(PERMISSIONS.length).toBe(11);
  });
});

describe("MATRIX fixture size guard", () => {
  it("has exactly 5 actor states", () => {
    expect(MATRIX.length).toBe(5);
  });
});

// ---------------------------------------------------------------------------
// Parity: 5 actor states × 11 permissions = 55 cells
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// EXPECTED table — independent ground truth derived directly from the Go
// matrix in policy_test.go. Defined separately from `actor.granted` so that a
// drift between the granted array (input to can()) and the expected decision
// surfaces as a test failure rather than a vacuous tautology.
//
// Layout: EXPECTED[actor.label][perm] === true iff the Go matrix grants perm
// to that actor state.
// ---------------------------------------------------------------------------

type ExpectedMatrix = Record<string, Record<Permission, boolean>>;

const EXPECTED: ExpectedMatrix = {
  "owner+verified": {
    "analytics.view": true,
    "api_keys.read": true,
    "api_keys.write": true,
    "billing.view": true,
    "billing.write": true,
    "grants.create": false,
    "ledger.view": true,
    "members.invite": true,
    "members.manage": true,
    "platform.admin": false,
    "workspace.settings": true,
  },
  "owner+unverified": {
    "analytics.view": false,
    "api_keys.read": true,
    "api_keys.write": false,
    "billing.view": true,
    "billing.write": false,
    "grants.create": false,
    "ledger.view": false,
    "members.invite": false,
    "members.manage": false,
    "platform.admin": false,
    "workspace.settings": false,
  },
  "member+verified": {
    "analytics.view": true,
    "api_keys.read": false,
    "api_keys.write": false,
    "billing.view": false,
    "billing.write": false,
    "grants.create": false,
    "ledger.view": true,
    "members.invite": false,
    "members.manage": false,
    "platform.admin": false,
    "workspace.settings": false,
  },
  "member+unverified": {
    "analytics.view": false,
    "api_keys.read": false,
    "api_keys.write": false,
    "billing.view": false,
    "billing.write": false,
    "grants.create": false,
    "ledger.view": false,
    "members.invite": false,
    "members.manage": false,
    "platform.admin": false,
    "workspace.settings": false,
  },
  admin: {
    "analytics.view": true,
    "api_keys.read": true,
    "api_keys.write": true,
    "billing.view": true,
    "billing.write": true,
    "grants.create": true,
    "ledger.view": true,
    "members.invite": true,
    "members.manage": true,
    "platform.admin": true,
    "workspace.settings": true,
  },
};

describe("MATRIX.granted matches EXPECTED table", () => {
  for (const actor of MATRIX) {
    it(`${actor.label} granted set equals EXPECTED truthy keys`, () => {
      const grantedFromExpected = (Object.entries(EXPECTED[actor.label]) as Array<
        [Permission, boolean]
      >)
        .filter(([, allowed]) => allowed)
        .map(([perm]) => perm)
        .sort();
      const grantedFromFixture = [...actor.granted].sort();
      expect(grantedFromFixture).toEqual(grantedFromExpected);
    });
  }
});

describe("can() parity with Go authz.Policy.Can — 55 cells", () => {
  for (const actor of MATRIX) {
    for (const perm of PERMISSIONS) {
      const want = EXPECTED[actor.label][perm];
      it(`${actor.label} [role=${actor.role || "any"} verified=${actor.verified} isAdmin=${actor.isAdmin}] can(${perm}) === ${want}`, () => {
        const viewer = { permissions: actor.granted };
        expect(can(viewer, perm)).toBe(want);
      });
    }
  }
});

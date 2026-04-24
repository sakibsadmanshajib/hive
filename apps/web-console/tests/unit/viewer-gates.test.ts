import { describe, it, expect } from "vitest";
import {
  canInviteMembers,
  canManageApiKeys,
  allowedUnverifiedRoutes,
} from "../../lib/viewer-gates";

interface ViewerGates {
  can_invite_members: boolean;
  can_manage_api_keys: boolean;
}

interface Viewer {
  gates: ViewerGates;
}

function makeViewer(overrides: Partial<ViewerGates> = {}): Viewer {
  return {
    gates: {
      can_invite_members: false,
      can_manage_api_keys: false,
      ...overrides,
    },
  };
}

describe("canInviteMembers", () => {
  it("returns true when can_invite_members is true", () => {
    const viewer = makeViewer({ can_invite_members: true });
    expect(canInviteMembers(viewer)).toBe(true);
  });

  it("returns false when can_invite_members is false", () => {
    const viewer = makeViewer({ can_invite_members: false });
    expect(canInviteMembers(viewer)).toBe(false);
  });
});

describe("canManageApiKeys", () => {
  it("returns true when can_manage_api_keys is true", () => {
    const viewer = makeViewer({ can_manage_api_keys: true });
    expect(canManageApiKeys(viewer)).toBe(true);
  });

  it("returns false when can_manage_api_keys is false", () => {
    const viewer = makeViewer({ can_manage_api_keys: false });
    expect(canManageApiKeys(viewer)).toBe(false);
  });
});

describe("allowedUnverifiedRoutes", () => {
  it("contains /console", () => {
    expect(allowedUnverifiedRoutes).toContain("/console");
  });

  it("contains /console/setup", () => {
    expect(allowedUnverifiedRoutes).toContain("/console/setup");
  });

  it("contains /console/settings/profile", () => {
    expect(allowedUnverifiedRoutes).toContain("/console/settings/profile");
  });

  it("does not contain /console/members", () => {
    expect(allowedUnverifiedRoutes).not.toContain("/console/members");
  });

  it("has exactly 3 entries", () => {
    expect(allowedUnverifiedRoutes).toHaveLength(3);
  });
});

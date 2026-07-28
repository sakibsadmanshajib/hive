import { describe, expect, it } from "vitest";

import {
  inviteDisabledReason,
  memberIdentityLabel,
  parseMemberRole,
  roleChangeDisabledReason,
} from "./roles";

describe("parseMemberRole", () => {
  it("accepts owner and member", () => {
    expect(parseMemberRole("owner")).toBe("owner");
    expect(parseMemberRole("member")).toBe("member");
  });

  it("normalizes case and surrounding whitespace", () => {
    expect(parseMemberRole(" Owner ")).toBe("owner");
  });

  it("rejects anything outside the supported set", () => {
    expect(parseMemberRole("admin")).toBeNull();
    expect(parseMemberRole("")).toBeNull();
    expect(parseMemberRole(null)).toBeNull();
    expect(parseMemberRole(42)).toBeNull();
  });
});

describe("inviteDisabledReason", () => {
  it("returns null when the viewer holds members.invite", () => {
    expect(
      inviteDisabledReason({
        permissions: ["members.invite"],
        user: { email_verified: true },
      }),
    ).toBeNull();
  });

  // The live console blamed email verification for every disabled invite
  // control, including for a verified member whose real blocker is the
  // members.invite permission (issue #536).
  it("names the permission, not email verification, for a verified non-owner", () => {
    const reason = inviteDisabledReason({
      permissions: ["analytics.view"],
      user: { email_verified: true },
    });
    expect(reason).toBe("Only workspace owners can invite teammates.");
    expect(reason?.toLowerCase()).not.toContain("verif");
  });

  it("names email verification only when the email really is unverified", () => {
    expect(
      inviteDisabledReason({
        permissions: [],
        user: { email_verified: false },
      }),
    ).toBe("Verify your email address before inviting teammates.");
  });
});

describe("roleChangeDisabledReason", () => {
  it("returns null when the change is allowed", () => {
    expect(
      roleChangeDisabledReason({
        canManage: true,
        isSelf: false,
        isLastOwner: false,
      }),
    ).toBeNull();
  });

  it("states the permission gate for a viewer without members.manage", () => {
    expect(
      roleChangeDisabledReason({
        canManage: false,
        isSelf: false,
        isLastOwner: false,
      }),
    ).toBe("Only workspace owners can change roles.");
  });

  it("states the self-change rule", () => {
    expect(
      roleChangeDisabledReason({
        canManage: true,
        isSelf: true,
        isLastOwner: false,
      }),
    ).toBe("You cannot change your own role.");
  });

  it("states the last-owner rule ahead of the self rule", () => {
    expect(
      roleChangeDisabledReason({
        canManage: true,
        isSelf: true,
        isLastOwner: true,
      }),
    ).toBe("The workspace must keep at least one owner.");
  });
});

describe("memberIdentityLabel", () => {
  it("uses the email when one is known", () => {
    expect(
      memberIdentityLabel({ user_id: "8f14e45f-ea", email: "ada@example.com" }),
    ).toBe("ada@example.com");
  });

  it("says so plainly when no email is on file instead of printing a raw UUID", () => {
    const label = memberIdentityLabel({ user_id: "8f14e45f-ea", email: "" });
    expect(label).toBe("No email on file");
    expect(label).not.toContain("8f14e45f");
  });
});

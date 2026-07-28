// Shared vocabulary for workspace membership roles plus the copy that explains
// why a members control is unavailable. Kept in one place so the console never
// states a reason the server does not actually enforce (issues #535, #536).
//
// The control-plane remains the authority: everything here mirrors
// accounts.NormalizeRole and the authz policy so the UI can pre-empt a refusal,
// never so it can grant anything.

export const MEMBER_ROLES = ["member", "owner"] as const;

export type MemberRole = (typeof MEMBER_ROLES)[number];

export const MEMBER_ROLE_LABELS: Record<MemberRole, string> = {
  member: "Member",
  owner: "Owner",
};

export const MEMBER_ROLE_HINTS: Record<MemberRole, string> = {
  member: "Can view analytics and usage.",
  owner: "Full control: billing, API keys, and members.",
};

/**
 * Narrows caller-supplied input to a supported role. Returns null for anything
 * else, including an empty value, so route handlers reject rather than guess.
 */
export function parseMemberRole(raw: unknown): MemberRole | null {
  if (typeof raw !== "string") return null;
  const normalized = raw.trim().toLowerCase();
  return (MEMBER_ROLES as readonly string[]).includes(normalized)
    ? (normalized as MemberRole)
    : null;
}

export interface InviteGateViewer {
  permissions: string[];
  user: { email_verified: boolean };
}

/**
 * Returns the real reason the viewer cannot invite teammates, or null when they
 * can. The console previously blamed email verification for every disabled
 * invite control, including for a verified member whose actual blocker is the
 * members.invite permission (issue #536).
 */
export function inviteDisabledReason(viewer: InviteGateViewer): string | null {
  if (viewer.permissions.includes("members.invite")) return null;
  if (!viewer.user.email_verified) {
    return "Verify your email address before inviting teammates.";
  }
  return "Only workspace owners can invite teammates.";
}

export interface RoleChangeGate {
  canManage: boolean;
  isSelf: boolean;
  isLastOwner: boolean;
}

/**
 * Returns the real reason a member's role cannot be changed, or null when it
 * can. Order matches the control-plane's refusal order so the console and the
 * server never disagree about which rule applies.
 */
export function roleChangeDisabledReason(gate: RoleChangeGate): string | null {
  if (!gate.canManage) return "Only workspace owners can change roles.";
  if (gate.isLastOwner) return "The workspace must keep at least one owner.";
  if (gate.isSelf) return "You cannot change your own role.";
  return null;
}

/**
 * Human identity for a member row. Falls back to plain language rather than
 * printing a raw UUID at the customer (issue #536).
 */
export function memberIdentityLabel(member: {
  user_id: string;
  email: string;
}): string {
  return member.email.trim() !== "" ? member.email : "No email on file";
}

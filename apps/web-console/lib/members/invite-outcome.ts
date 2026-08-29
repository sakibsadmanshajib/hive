import type { InvitationDelivery } from "@/lib/control-plane/client";

// What the console is allowed to say after an invitation is issued.
//
// This module exists because the console used to say "Invitation sent" every
// time, while no email transport existed anywhere in the product and the proxy
// route discarded the only copy of the acceptance token (issue #1440). The
// wording is now a pure function of what the control-plane reports actually
// happened, so there is no branch that can claim a delivery on its own.
//
// Kept free of React so the claim itself can be asserted in a unit test.

export interface InvitationOutcome {
  tone: "success" | "warning";
  // What happened. This sentence is the one that must never claim a delivery
  // that did not occur.
  message: string;
  // What the inviting user should do next, kept separate from the message
  // because the two surfaces that render this can offer different next steps:
  // the invite panel can show the link, the no-JavaScript redirect cannot.
  action: string | null;
  // Whether the interface must put the invitation link in front of the inviting
  // user so they can deliver it themselves. True whenever nothing was mailed,
  // and true after a successful send as well, because a message that is
  // delivered to a spam folder is indistinguishable from one that is not
  // delivered at all.
  showLink: boolean;
}

// Phrases that assert a message reached somebody. A non-"sent" outcome must
// contain none of them. Exported so the guard test and the copy cannot drift
// apart: adding a new way to claim delivery means adding it here.
export const DELIVERY_CLAIM_PATTERNS: readonly RegExp[] = [
  /\bwe emailed\b/i,
  /\bwe sent\b/i,
  /\binvitation sent\b/i,
  /\bemail sent\b/i,
  /\bsent (?:an? )?(?:email|invitation|invite)\b/i,
  /\bhas been (?:sent|emailed)\b/i,
];

function recipient(email: string | null): string {
  const trimmed = (email ?? "").trim();
  return trimmed === "" ? "this person" : trimmed;
}

export function invitationOutcome(
  delivery: InvitationDelivery,
  email: string | null,
): InvitationOutcome {
  switch (delivery) {
    case "sent":
      return {
        tone: "success",
        message: `We emailed an invitation to ${recipient(email)}. They join this workspace once they accept.`,
        action: null,
        showLink: true,
      };
    case "not_configured":
      return {
        tone: "warning",
        message: `The invitation for ${recipient(email)} is ready, but this deployment has no mail delivery configured, so nothing was emailed.`,
        action: "Pass the link on yourself.",
        showLink: true,
      };
    case "failed":
    default:
      return {
        tone: "warning",
        message: `The invitation for ${recipient(email)} is ready, but the mail relay refused it, so nothing reached them.`,
        action: "Pass the link on yourself.",
        showLink: true,
      };
  }
}

// parseDeliveryFlag reads the outcome off the no-JavaScript redirect. Anything
// unrecognised is treated as a failure rather than a success, because a claim of
// delivery has to be earned.
export function parseDeliveryFlag(raw: string | undefined): InvitationDelivery | null {
  switch (raw) {
    case "sent":
      return "sent";
    case "not_configured":
      return "not_configured";
    case "failed":
      return "failed";
    case undefined:
    case "":
      return null;
    default:
      return "failed";
  }
}

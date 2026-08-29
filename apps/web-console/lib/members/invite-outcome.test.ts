import { describe, expect, it } from "vitest";

import {
  DELIVERY_CLAIM_PATTERNS,
  invitationOutcome,
  parseDeliveryFlag,
} from "./invite-outcome";
import type { InvitationDelivery } from "@/lib/control-plane/client";

// THE GUARD FOR ISSUE #1440, ON THE CONSOLE SIDE.
//
// The defect was a success claim with nothing behind it. The console rendered
// "Invitation sent. They join this workspace once they accept." on every
// successful write, while no email transport existed anywhere in the product.
// So the invariant is narrow: only the outcome that means a transport ran and
// succeeded may claim that anything was sent.
//
// The primary assertion is an allowlist, not a blacklist. Review pointed out
// that a list of forbidden phrases is a list somebody eventually walks past
// with novel wording, and that a blacklist guarding a correctness claim will
// drift. So every message is pinned exactly: changing any of this copy fails
// here and has to be a deliberate act, at which point the author is looking
// straight at whether the new wording claims a delivery. The phrase patterns
// stay as a second net, because they also catch a message assembled at runtime
// rather than written as a literal.

const EXPECTED: Record<InvitationDelivery, { tone: string; message: string; action: string | null }> = {
  sent: {
    tone: "success",
    message:
      "We emailed an invitation to invitee@example.test. They join this workspace once they accept.",
    action: null,
  },
  not_configured: {
    tone: "warning",
    message:
      "The invitation for invitee@example.test is ready, but this deployment has no mail delivery configured, so nothing was emailed.",
    action: "Pass the link on yourself.",
  },
  failed: {
    tone: "warning",
    message:
      "The invitation for invitee@example.test is ready, but the mail relay refused it, so nothing reached them.",
    action: "Pass the link on yourself.",
  },
};

describe("invitationOutcome", () => {
  it.each(Object.keys(EXPECTED) as InvitationDelivery[])(
    "renders exactly the approved copy for outcome %s",
    (delivery) => {
      const outcome = invitationOutcome(delivery, "invitee@example.test");
      expect(outcome).toEqual(EXPECTED[delivery]);
    },
  );

  it("claims a delivery only for the one outcome that means a transport succeeded", () => {
    // Derived from the same table rather than restated, so a new outcome added
    // to the type cannot slip past this by being forgotten here.
    const claiming = (Object.keys(EXPECTED) as InvitationDelivery[]).filter(
      (delivery) => {
        const { message, action } = EXPECTED[delivery];
        const full = `${message} ${action ?? ""}`;
        return DELIVERY_CLAIM_PATTERNS.some((pattern) => pattern.test(full));
      },
    );
    expect(claiming).toEqual(["sent"]);
  });

  it.each(["not_configured", "failed"] as const)(
    "tells the user what to do instead for outcome %s",
    (delivery) => {
      const outcome = invitationOutcome(delivery, "invitee@example.test");
      expect(outcome.tone).toBe("warning");
      expect(outcome.action).not.toBeNull();
    },
  );

  it("stays readable when the address is missing", () => {
    const outcome = invitationOutcome("not_configured", null);
    expect(outcome.message).not.toContain("null");
    expect(outcome.message).not.toContain("undefined");
    expect(outcome.message).toContain("this person");
  });
});

describe("parseDeliveryFlag", () => {
  it("reads the three real outcomes", () => {
    expect(parseDeliveryFlag("sent")).toBe("sent");
    expect(parseDeliveryFlag("not_configured")).toBe("not_configured");
    expect(parseDeliveryFlag("failed")).toBe("failed");
  });

  it("returns null when no invitation was just issued", () => {
    expect(parseDeliveryFlag(undefined)).toBeNull();
    expect(parseDeliveryFlag("")).toBeNull();
  });

  it("degrades an unrecognised value to failure, never to a claim of delivery", () => {
    // Including the old flag this replaced. A stale link or bookmark carrying
    // `invited=1` must not resurrect the claim the flag used to make.
    expect(parseDeliveryFlag("1")).toBe("failed");
    expect(parseDeliveryFlag("yes")).toBe("failed");
  });
});

import { describe, expect, it } from "vitest";

import {
  DELIVERY_CLAIM_PATTERNS,
  invitationOutcome,
  parseDeliveryFlag,
} from "./invite-outcome";

// THE GUARD FOR ISSUE #1440, ON THE CONSOLE SIDE.
//
// The defect was a success claim with nothing behind it. The console rendered
// "Invitation sent. They join this workspace once they accept." on every
// successful write, while no email transport existed anywhere in the product.
// So the invariant is narrow: only the outcome that means a transport ran and
// succeeded may claim that anything was sent.

describe("invitationOutcome", () => {
  it("claims a delivery only when the control-plane reported one", () => {
    const sent = invitationOutcome("sent", "invitee@example.test");
    expect(sent.tone).toBe("success");
    expect(sent.message).toMatch(/emailed an invitation to invitee@example\.test/i);
  });

  it.each(["not_configured", "failed"] as const)(
    "never claims an email was sent for outcome %s",
    (delivery) => {
      const outcome = invitationOutcome(delivery, "invitee@example.test");
      for (const pattern of DELIVERY_CLAIM_PATTERNS) {
        expect(
          pattern.test(outcome.message),
          `outcome ${delivery} claims a delivery: ${outcome.message}`,
        ).toBe(false);
      }
    },
  );

  it.each(["not_configured", "failed"] as const)(
    "tells the user what to do instead for outcome %s",
    (delivery) => {
      const outcome = invitationOutcome(delivery, "invitee@example.test");
      expect(outcome.tone).toBe("warning");
      expect(outcome.action).not.toBeNull();
      expect(outcome.message).toMatch(/nothing (?:was emailed|reached them)/i);
    },
  );

  it("stays readable when the address is missing", () => {
    const outcome = invitationOutcome("not_configured", null);
    expect(outcome.message).not.toContain("null");
    expect(outcome.message).not.toContain("undefined");
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

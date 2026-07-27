/**
 * Guards the auth-error sanitization boundary. The concrete regression: a user
 * with no active tenant membership used to get an auth-hook failure whose raw
 * text ("Error running hook URI: pg-functions://postgres/public/...") was
 * rendered straight into the sign-in form.
 *
 * The filter is an allow-list, so the important half of this suite is the
 * "replaces" half: it asserts the default is to withhold. Each case there is a
 * string a deny-list would have had to predict, which is exactly why the
 * allow-list is the safer shape.
 */
import { describe, expect, it } from "vitest";
import { toUserFacingAuthMessage } from "./auth-error";

// Fixed clock so the support reference is deterministic.
const AT = new Date("2026-07-27T14:03:22.512Z");
const GENERIC =
  "Something went wrong on our end. Please try again in a moment, and contact support if it keeps happening. Reference AUTH-20260727T140322Z.";

describe("toUserFacingAuthMessage", () => {
  describe("withholds anything not on the allow-list", () => {
    it("replaces the raw auth-hook URI message", () => {
      expect(
        toUserFacingAuthMessage(
          "Error running hook URI: pg-functions://postgres/public/custom_access_token_hook",
          AT,
        ),
      ).toBe(GENERIC);
    });

    it("replaces a bare no_active_membership exception", () => {
      expect(toUserFacingAuthMessage("no_active_membership", AT)).toBe(GENERIC);
    });

    it("replaces a Postgres connection-string style message", () => {
      expect(
        toUserFacingAuthMessage(
          "failed to connect to postgres://user@db.internal:5432/hive",
          AT,
        ),
      ).toBe(GENERIC);
    });

    it("replaces a message carrying a SQLSTATE code", () => {
      expect(toUserFacingAuthMessage("raise_exception (SQLSTATE P0001)", AT)).toBe(
        GENERIC,
      );
    });

    // The four cases below are the ones the previous deny-list let through.
    it("replaces a unique-constraint violation", () => {
      expect(
        toUserFacingAuthMessage(
          'duplicate key value violates unique constraint "users_email_key"',
          AT,
        ),
      ).toBe(GENERIC);
    });

    it("replaces a syntax error", () => {
      expect(
        toUserFacingAuthMessage('syntax error at or near "SELECT"', AT),
      ).toBe(GENERIC);
    });

    it("replaces a message naming a relation", () => {
      expect(
        toUserFacingAuthMessage('relation "tenant_users" does not exist', AT),
      ).toBe(GENERIC);
    });

    it("replaces a message naming a column", () => {
      expect(
        toUserFacingAuthMessage(
          'column "selected_tenant_id" of relation "users" does not exist',
          AT,
        ),
      ).toBe(GENERIC);
    });

    it("replaces an unrecognized but innocuous-looking message", () => {
      expect(toUserFacingAuthMessage("something unexpected happened", AT)).toBe(
        GENERIC,
      );
    });

    it("replaces a safe message padded into a longer disclosure", () => {
      expect(
        toUserFacingAuthMessage(
          "Invalid login credentials for role supabase_auth_admin on db.internal",
          AT,
        ),
      ).toBe(GENERIC);
    });
  });

  describe("passes through known GoTrue copy", () => {
    it.each([
      "Invalid login credentials",
      "Invalid credentials",
      "Email not confirmed",
      "User already registered",
      "Password should be at least 12 characters",
      "Token has expired or is invalid",
      "Email link is invalid or has expired",
      "Email rate limit exceeded",
      "For security purposes, you can only request this after 51 seconds",
      "Signups not allowed for this instance",
      "New password should be different from the old password",
      "Unable to validate email address: invalid format",
    ])("keeps %s unchanged", (message) => {
      expect(toUserFacingAuthMessage(message, AT)).toBe(message);
    });

    it("trims surrounding whitespace on a safe message", () => {
      expect(toUserFacingAuthMessage("  Email not confirmed  ", AT)).toBe(
        "Email not confirmed",
      );
    });
  });

  describe("empty and oversized input", () => {
    it("returns the fallback for null", () => {
      expect(toUserFacingAuthMessage(null, AT)).toBe(GENERIC);
    });

    it("returns the fallback for undefined", () => {
      expect(toUserFacingAuthMessage(undefined, AT)).toBe(GENERIC);
    });

    it("returns the fallback for a whitespace-only message", () => {
      expect(toUserFacingAuthMessage("   ", AT)).toBe(GENERIC);
    });

    it("returns the fallback for an over-long message", () => {
      expect(toUserFacingAuthMessage("a".repeat(201), AT)).toBe(GENERIC);
    });
  });

  describe("support reference", () => {
    it("is second-precision UTC so it can be matched against the Supabase auth log", () => {
      expect(toUserFacingAuthMessage("unknown", AT)).toContain(
        "AUTH-20260727T140322Z",
      );
    });

    it("changes with the clock", () => {
      expect(
        toUserFacingAuthMessage("unknown", new Date("2026-07-28T01:02:03.000Z")),
      ).toContain("AUTH-20260728T010203Z");
    });

    it("is not attached to a message that passed the allow-list", () => {
      expect(toUserFacingAuthMessage("Email not confirmed", AT)).not.toContain(
        "AUTH-",
      );
    });
  });
});

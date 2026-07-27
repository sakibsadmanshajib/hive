/**
 * Guards the auth-error sanitization boundary. The concrete regression: a user
 * with no active tenant membership used to get an auth-hook failure whose raw
 * text ("Error running hook URI: pg-functions://postgres/public/...") was
 * rendered straight into the sign-in form.
 */
import { describe, expect, it } from "vitest";
import { toUserFacingAuthMessage } from "./auth-error";

// Duplicated rather than imported on purpose: this pins the exact copy a user
// sees, so changing the message has to be a deliberate edit in two places.
const GENERIC =
  "Something went wrong on our end. Please try again in a moment, and contact support if it keeps happening.";

describe("toUserFacingAuthMessage", () => {
  it("replaces the raw auth-hook URI message", () => {
    expect(
      toUserFacingAuthMessage(
        "Error running hook URI: pg-functions://postgres/public/custom_access_token_hook",
      ),
    ).toBe(GENERIC);
  });

  it("replaces a bare no_active_membership exception", () => {
    expect(toUserFacingAuthMessage("no_active_membership")).toBe(GENERIC);
  });

  it("replaces a Postgres connection-string style message", () => {
    expect(
      toUserFacingAuthMessage(
        "failed to connect to postgres://user@db.internal:5432/hive",
      ),
    ).toBe(GENERIC);
  });

  it("replaces a message carrying a SQLSTATE code", () => {
    expect(
      toUserFacingAuthMessage("raise_exception (SQLSTATE P0001)"),
    ).toBe(GENERIC);
  });

  it("passes through Invalid login credentials unchanged", () => {
    expect(toUserFacingAuthMessage("Invalid login credentials")).toBe(
      "Invalid login credentials",
    );
  });

  it("passes through Email not confirmed unchanged", () => {
    expect(toUserFacingAuthMessage("Email not confirmed")).toBe(
      "Email not confirmed",
    );
  });

  it("trims surrounding whitespace on a safe message", () => {
    expect(toUserFacingAuthMessage("  Email not confirmed  ")).toBe(
      "Email not confirmed",
    );
  });

  it("returns the fallback for null", () => {
    expect(toUserFacingAuthMessage(null)).toBe(GENERIC);
  });

  it("returns the fallback for undefined", () => {
    expect(toUserFacingAuthMessage(undefined)).toBe(GENERIC);
  });

  it("returns the fallback for a whitespace-only message", () => {
    expect(toUserFacingAuthMessage("   ")).toBe(GENERIC);
  });

  it("returns the fallback for an over-long message", () => {
    expect(toUserFacingAuthMessage("a".repeat(201))).toBe(GENERIC);
  });

  it("keeps a message that sits exactly on the length cap", () => {
    const onCap = "b".repeat(200);
    expect(toUserFacingAuthMessage(onCap)).toBe(onCap);
  });
});

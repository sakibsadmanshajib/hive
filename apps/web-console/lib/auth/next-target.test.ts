import { describe, expect, it } from "vitest";
import { appendNextParam, resolveNextTarget } from "./next-target";

describe("resolveNextTarget", () => {
  it("defaults to /console when next is null", () => {
    expect(resolveNextTarget(null)).toBe("/console");
  });

  it("defaults to /console when next is empty string", () => {
    expect(resolveNextTarget("")).toBe("/console");
  });

  it("allows the exact /invitations/accept path", () => {
    expect(resolveNextTarget("/invitations/accept")).toBe(
      "/invitations/accept",
    );
  });

  // /auth/callback (email-confirmation redirect) shares this same allow-list
  // so both post-login (sign-in) and post-verification (callback) redirect
  // checks stay in lockstep instead of drifting apart.
  it("allows the exact /console/settings/profile path", () => {
    expect(resolveNextTarget("/console/settings/profile")).toBe(
      "/console/settings/profile",
    );
  });

  it("allows the exact /auth/reset-password path", () => {
    expect(resolveNextTarget("/auth/reset-password")).toBe(
      "/auth/reset-password",
    );
  });

  // Issue #534: an invitee who is not signed in gets bounced to sign-in, and
  // the acceptance token travels in the `next` value. Dropping the query string
  // made acceptance impossible for anyone without an existing account.
  it("allows /invitations/accept with its token query preserved", () => {
    expect(resolveNextTarget("/invitations/accept?token=abc-123")).toBe(
      "/invitations/accept?token=abc-123",
    );
  });

  it("rejects a path that only starts with /invitations/accept as a substring", () => {
    expect(resolveNextTarget("/invitations/accept-evil")).toBe("/console");
  });

  it("rejects an off-site redirect dressed up as an invitation path", () => {
    expect(
      resolveNextTarget("https://evil.example.com/invitations/accept?token=x"),
    ).toBe("/console");
    expect(resolveNextTarget("//evil.example.com/invitations/accept")).toBe(
      "/console",
    );
  });

  it("allows /oauth/consent with a query string", () => {
    expect(
      resolveNextTarget("/oauth/consent?authorization_id=abc-123"),
    ).toBe("/oauth/consent?authorization_id=abc-123");
  });

  it("allows /oauth/consent with no query string", () => {
    expect(resolveNextTarget("/oauth/consent")).toBe("/oauth/consent");
  });

  it("rejects an arbitrary unlisted relative path", () => {
    expect(resolveNextTarget("/some/random/page")).toBe("/console");
  });

  it("rejects a path that merely starts with an allowed prefix as a substring, not a real segment", () => {
    expect(resolveNextTarget("/oauth/consent-evil")).toBe("/console");
  });

  it("rejects an absolute URL (open-redirect attempt)", () => {
    expect(resolveNextTarget("https://evil.example.com")).toBe("/console");
  });

  it("rejects a protocol-relative URL (open-redirect attempt)", () => {
    expect(resolveNextTarget("//evil.example.com")).toBe("/console");
  });
});

describe("appendNextParam", () => {
  it("returns the path unchanged when next is null", () => {
    expect(appendNextParam("/auth/sign-up", null)).toBe("/auth/sign-up");
  });

  it("returns the path unchanged when next is empty string", () => {
    expect(appendNextParam("/auth/sign-up", "")).toBe("/auth/sign-up");
  });

  it("appends next as a query param, URI-encoded", () => {
    expect(
      appendNextParam(
        "/auth/sign-up",
        "/oauth/consent?authorization_id=abc-123",
      ),
    ).toBe(
      "/auth/sign-up?next=%2Foauth%2Fconsent%3Fauthorization_id%3Dabc-123",
    );
  });

  it("uses & when the path already has a query string", () => {
    expect(appendNextParam("/auth/callback?code=abc", "/console")).toBe(
      "/auth/callback?code=abc&next=%2Fconsole",
    );
  });
});

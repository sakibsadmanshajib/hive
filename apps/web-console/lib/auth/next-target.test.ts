import { describe, expect, it } from "vitest";
import { resolveNextTarget } from "./next-target";

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

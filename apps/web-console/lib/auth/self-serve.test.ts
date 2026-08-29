/**
 * Guards the deployment flag behind the sign-up route (issue #1328).
 *
 * The important half is the default: unset and empty must read as disabled.
 * An unset build arg reaches next build as an empty string, so a flag that
 * treats empty as "signup works" would put the form back on every deployment
 * that never sets the variable, which is the state this exists to stop.
 */
import { afterEach, describe, expect, it } from "vitest";

import { isSelfServeSignupEnabled } from "./self-serve";

const KEY = "NEXT_PUBLIC_DISABLE_SELF_SERVE_SIGNUP";
const original = process.env[KEY];

afterEach(() => {
  if (original === undefined) {
    delete process.env[KEY];
  } else {
    process.env[KEY] = original;
  }
});

describe("isSelfServeSignupEnabled", () => {
  it("is disabled when the variable is unset", () => {
    delete process.env[KEY];
    expect(isSelfServeSignupEnabled()).toBe(false);
  });

  it("is disabled when the variable is an empty string", () => {
    process.env[KEY] = "";
    expect(isSelfServeSignupEnabled()).toBe(false);
  });

  it("is disabled when the deployment says signup is disabled", () => {
    process.env[KEY] = "true";
    expect(isSelfServeSignupEnabled()).toBe(false);
  });

  it("is enabled when the deployment says so explicitly", () => {
    process.env[KEY] = "false";
    expect(isSelfServeSignupEnabled()).toBe(true);
  });

  it("accepts 0 as the numeric spelling of false", () => {
    process.env[KEY] = "0";
    expect(isSelfServeSignupEnabled()).toBe(true);
  });

  it("is case and whitespace insensitive, since compose passes the value through verbatim", () => {
    process.env[KEY] = "  False  ";
    expect(isSelfServeSignupEnabled()).toBe(true);
  });

  it("treats an unrecognized value as disabled rather than guessing", () => {
    process.env[KEY] = "maybe";
    expect(isSelfServeSignupEnabled()).toBe(false);
  });
});

// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";
import { beforeEach, describe, expect, it } from "vitest";

import { AUTH_STORAGE_KEY, clearAuthSession, readAuthSession, writeAuthSession } from "../src/features/auth/auth-session";

describe("auth session", () => {
  beforeEach(() => {
    window.localStorage.clear();
  });

  it("persists and reads session payload", () => {
    writeAuthSession({ accessToken: "sk_test", email: "demo@example.com", name: "Demo" });

    expect(window.localStorage.getItem(AUTH_STORAGE_KEY)).toEqual(
      JSON.stringify({ accessToken: "sk_test", email: "demo@example.com", name: "Demo" }),
    );

    expect(readAuthSession()).toEqual({
      accessToken: "sk_test",
      email: "demo@example.com",
      name: "Demo",
    });
  });

  it("clears persisted session", () => {
    writeAuthSession({ accessToken: "sk_test", email: "demo@example.com" });

    clearAuthSession();

    expect(readAuthSession()).toBeNull();
    expect(window.localStorage.getItem(AUTH_STORAGE_KEY)).toBeNull();
  });
});

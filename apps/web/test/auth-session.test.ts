// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";
import { beforeEach, describe, expect, it } from "vitest";

import { clearAuthSession, readAuthSession, writeAuthSession } from "../src/features/auth/auth-session";

describe("auth session", () => {
  beforeEach(() => {
    window.localStorage.clear();
  });

  it("persists and reads session payload", () => {
    writeAuthSession({ apiKey: "sk_test", email: "demo@example.com", name: "Demo" });

    expect(readAuthSession()).toEqual({
      apiKey: "sk_test",
      email: "demo@example.com",
      name: "Demo",
    });
  });

  it("clears persisted session", () => {
    writeAuthSession({ apiKey: "sk_test", email: "demo@example.com" });

    clearAuthSession();

    expect(readAuthSession()).toBeNull();
  });
});

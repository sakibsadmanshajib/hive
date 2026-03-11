// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";
import { render, screen, waitFor } from "@testing-library/react";
import { createElement } from "react";
import { beforeEach, describe, expect, it } from "vitest";

import {
  AUTH_STORAGE_KEY,
  clearAuthSession,
  readAuthSession,
  useAuthSession,
  writeAuthSession,
} from "../src/features/auth/auth-session";

function SessionProbe() {
  const session = useAuthSession();
  return createElement("div", null, session?.accessToken ?? "none");
}

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

  it("updates subscribers when the auth session changes in the same tab", async () => {
    render(createElement(SessionProbe));

    expect(screen.getByText("none")).toBeInTheDocument();

    writeAuthSession({ accessToken: "fresh_token", email: "demo@example.com" });

    await waitFor(() => {
      expect(screen.getByText("fresh_token")).toBeInTheDocument();
    });

    clearAuthSession();

    await waitFor(() => {
      expect(screen.getByText("none")).toBeInTheDocument();
    });
  });
});

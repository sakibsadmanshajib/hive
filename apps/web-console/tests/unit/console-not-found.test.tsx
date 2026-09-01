import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { NextIntlClientProvider } from "next-intl";

import { ConsoleNotFound } from "@/components/app-shell/console-not-found";
import type { Viewer } from "@/lib/control-plane/client";
import enMessages from "@/messages/en.json";

// Same reason as console-shell.test.tsx: LocaleSwitcher is an async Server
// Component and cannot render under plain React Testing Library.
vi.mock("@/components/locale-switcher", () => ({
  LocaleSwitcher: () => null,
}));

const APP_ROOT = join(dirname(fileURLToPath(import.meta.url)), "..", "..");

// The three surfaces whose 404 is access control rather than a data miss: they
// answer notFound() to a viewer without the role so that the response cannot
// confirm the surface exists (#947/#948/#949). ConsoleNotFound renders the full
// console shell, including the operator-only rail group, so putting it on any
// of these would hand back exactly what the 404 withholds.
const ROLE_GATED_PAGES = [
  "app/console/providers/page.tsx",
  "app/console/feature-gates/page.tsx",
  "app/console/marketplace/page.tsx",
];

const VIEWER: Viewer = {
  user: { id: "user_1", email: "owner@example.com", email_verified: true },
  current_account: {
    id: "acct_1",
    display_name: "Hive Demo",
    slug: "hive-demo",
    account_type: "individual",
    role: "owner",
  },
  memberships: [
    {
      account_id: "acct_1",
      account_display_name: "Hive Demo",
      account_slug: "hive-demo",
      display_name: "Hive Demo",
      role: "owner",
      status: "active",
    },
  ],
  permissions: ["analytics.view"],
};

describe("ConsoleNotFound", () => {
  it("renders a signed-in not-found state with a way back", () => {
    render(
      <NextIntlClientProvider locale="en" messages={enMessages}>
        <ConsoleNotFound
          viewer={VIEWER}
          ownerName="Owner"
          active="/console/catalog"
          section="Model catalog"
          eyebrow="Build"
          title="Model not found"
          description="No model on this workspace matches that address."
          backHref="/console/catalog"
          backLabel="Back to catalog"
        />
      </NextIntlClientProvider>,
    );

    expect(
      screen.getByRole("heading", { name: "Model not found" }),
    ).toBeTruthy();
    const back = screen.getByRole("link", { name: /back to catalog/i });
    expect(back.getAttribute("href")).toBe("/console/catalog");
  });
});

describe("role-gated console pages", () => {
  it("keep notFound() and never render the in-shell not-found", () => {
    for (const relativePath of ROLE_GATED_PAGES) {
      const source = readFileSync(join(APP_ROOT, relativePath), "utf8");
      expect(source, `${relativePath} must keep its notFound() gate`).toContain(
        "notFound()",
      );
      expect(
        source,
        `${relativePath} must not render the console shell for its 404`,
      ).not.toContain("ConsoleNotFound");
    }
  });
});

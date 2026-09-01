import { readdirSync, readFileSync, statSync } from "node:fs";
import { dirname, join, relative } from "node:path";
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
const CONSOLE_PAGES = join(APP_ROOT, "app", "console");

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

// A viewer who holds neither the platform-admin permission nor workspace
// ownership, which is what both role gates refuse.
const PLAIN_MEMBER: Viewer = {
  ...VIEWER,
  current_account: { ...VIEWER.current_account, role: "member" },
  permissions: ["analytics.view"],
};

vi.mock("next/headers", () => ({
  cookies: async () => ({ get: () => undefined }),
}));

vi.mock("@/lib/supabase/server", () => ({
  createClient: () => ({
    auth: {
      getUser: async () => ({ data: { user: { id: "user_1" } }, error: null }),
      getSession: async () => ({
        data: { session: { access_token: "test-token" } },
      }),
    },
  }),
}));

// Only getViewer is replaced. Everything else stays real, so a page that got
// past its gate would go on to make its actual control-plane calls and fail
// loudly rather than quietly rendering a stub.
vi.mock("@/lib/control-plane/client", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/control-plane/client")>()),
  getViewer: async () => PLAIN_MEMBER,
}));

// The surfaces whose 404 is access control rather than a data miss
// (#947/#948/#949). Each must refuse a viewer without the role by throwing the
// App Router not-found error, which carries the 404 in its digest, rather than
// returning any markup at all.
const ROLE_GATED_PAGES = [
  { file: "app/console/providers/page.tsx", module: "@/app/console/providers/page" },
  { file: "app/console/feature-gates/page.tsx", module: "@/app/console/feature-gates/page" },
  { file: "app/console/marketplace/page.tsx", module: "@/app/console/marketplace/page" },
];

// Every console page that calls one of the two role gates. Read off disk so a
// fourth role-gated surface added later fails this suite until it is added to
// the table above, instead of shipping unguarded.
function pagesBehindARoleGate(): string[] {
  const found: string[] = [];
  const walk = (dir: string) => {
    for (const entry of readdirSync(dir)) {
      const full = join(dir, entry);
      if (statSync(full).isDirectory()) {
        walk(full);
      } else if (entry === "page.tsx") {
        const source = readFileSync(full, "utf8");
        if (/is(PlatformAdmin|WorkspaceAdmin)Viewer\(/.test(source)) {
          found.push(relative(APP_ROOT, full).split("\\").join("/"));
        }
      }
    }
  };
  walk(CONSOLE_PAGES);
  return found.sort();
}

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
  // Behavioural, not a source grep. A page that grew its own inline 404 around
  // <ConsoleShell/> would satisfy any "does it still mention notFound" check
  // while handing back the operator-only rail; this fails it, because a page
  // that returns markup does not reject.
  for (const page of ROLE_GATED_PAGES) {
    it(`${page.file} refuses a viewer without the role with a 404`, async () => {
      const { default: Page } = await import(page.module);
      await expect(Page()).rejects.toThrow("NEXT_HTTP_ERROR_FALLBACK;404");
    });
  }

  it("covers every console page that sits behind a role gate", () => {
    expect(pagesBehindARoleGate()).toEqual(
      ROLE_GATED_PAGES.map((page) => page.file).sort(),
    );
  });
});

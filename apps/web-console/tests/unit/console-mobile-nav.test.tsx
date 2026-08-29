import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, within } from "@testing-library/react";
import { NextIntlClientProvider } from "next-intl";

import { ConsoleShell } from "@/components/app-shell/console-shell";
import NotFound from "@/app/not-found";
import { ObservabilityTiles } from "@/components/analytics/observability-tiles";
import { DataTable } from "@/components/ui/data-table";
import { formatDeltaPercent } from "@/components/analytics/analytics-overview-section";
import type { ViewerMembership } from "@/lib/control-plane/client";
import type { RoleGateViewer } from "@/lib/viewer-gates";
import enMessages from "@/messages/en.json";

// Same stub as tests/unit/console-shell.test.tsx: LocaleSwitcher is an async
// Server Component and cannot render under React Testing Library.
vi.mock("@/components/locale-switcher", () => ({
  LocaleSwitcher: () => null,
}));

const MEMBERSHIPS: ViewerMembership[] = [
  {
    account_id: "acct_1",
    account_display_name: "Hive Demo",
    account_slug: "hive-demo",
    display_name: "Hive Demo",
    role: "owner",
    status: "active",
  },
];

const PLATFORM_ADMIN: RoleGateViewer = {
  permissions: ["platform.admin"],
  current_account: { role: "owner" },
};

function renderShell() {
  return render(
    <NextIntlClientProvider locale="en" messages={enMessages}>
      <ConsoleShell
        workspace={{ id: "acct_1", name: "Hive Demo", slug: "hive-demo" }}
        memberships={MEMBERSHIPS}
        viewer={PLATFORM_ADMIN}
        user={{ email: "owner@example.com" }}
        active="/console/billing"
      >
        <p>page body</p>
      </ConsoleShell>
    </NextIntlClientProvider>,
  );
}

function sidebar(): HTMLElement {
  const element = document.getElementById("console-primary-nav");
  if (!element) {
    throw new Error("sidebar element not found");
  }
  return element;
}

// `hidden` is the whole defect in issue #1367: below lg the rail carried the
// class with nothing replacing it, so thirteen nav entries were display:none
// on a phone and Billing, Members, Analytics, Logs, API keys and Settings had
// no route from any page. jsdom applies no Tailwind, so the class -- not
// computed visibility -- is what these assertions read.
function sidebarIsHiddenOnSmallScreens(): boolean {
  return sidebar().className.split(/\s+/).includes("hidden");
}

describe("console mobile navigation (issue #1367)", () => {
  it("gives small screens a nav toggle that is collapsed on first paint", () => {
    renderShell();

    const toggle = screen.getByRole("button", { name: "Open navigation" });
    expect(toggle.getAttribute("aria-expanded")).toBe("false");
    expect(toggle.getAttribute("aria-controls")).toBe("console-primary-nav");
    expect(sidebarIsHiddenOnSmallScreens()).toBe(true);
  });

  it("opens the rail as a drawer and reaches every console section", () => {
    renderShell();

    fireEvent.click(screen.getByRole("button", { name: "Open navigation" }));

    const toggle = screen.getByRole("button", { name: "Close navigation" });
    expect(toggle.getAttribute("aria-expanded")).toBe("true");
    expect(sidebarIsHiddenOnSmallScreens()).toBe(false);

    // The routes the 375px audit found unreachable from every page.
    const nav = within(sidebar());
    for (const label of [
      "Billing",
      "Members",
      "Analytics",
      "Logs",
      "API keys",
      "Settings",
    ]) {
      expect(nav.getByRole("link", { name: label })).toBeTruthy();
    }
  });

  it("closes when a nav link is followed, so the drawer never covers the page it opened", () => {
    renderShell();

    fireEvent.click(screen.getByRole("button", { name: "Open navigation" }));
    fireEvent.click(within(sidebar()).getByRole("link", { name: "Members" }));

    expect(sidebarIsHiddenOnSmallScreens()).toBe(true);
    expect(
      screen.getByRole("button", { name: "Open navigation" }).getAttribute("aria-expanded"),
    ).toBe("false");
  });

  it("closes on Escape", () => {
    renderShell();

    fireEvent.click(screen.getByRole("button", { name: "Open navigation" }));
    fireEvent.keyDown(document, { key: "Escape" });

    expect(sidebarIsHiddenOnSmallScreens()).toBe(true);
  });
});

describe("data table horizontal overflow (issue #1367, second half)", () => {
  it("scrolls a wide table inside its own positioned container", () => {
    const { container } = render(
      <DataTable
        rows={[{ id: "r1" }]}
        rowKey={(row) => row.id}
        columns={[{ key: "id", header: "Id", cell: (row) => row.id }]}
      />,
    );

    const wrapper = container.firstElementChild;
    const classes = (wrapper?.className ?? "").split(/\s+/);
    // overflow-x-auto: the columns past a 375px fold have to stay reachable,
    // which overflow-hidden denied. relative: without a containing block, the
    // over-wide table pushes the whole document sideways in Chromium even
    // though the scroller is the right width. jsdom does no layout, so the
    // measured proof is in docs/proof; this guards the mechanism.
    expect(classes).toContain("overflow-x-auto");
    expect(classes).toContain("relative");
    expect(classes).not.toContain("overflow-hidden");
  });
});

describe("404 page", () => {
  it("is branded and offers a way back", () => {
    render(
      <NextIntlClientProvider locale="en" messages={enMessages}>
        <NotFound />
      </NextIntlClientProvider>,
    );

    expect(screen.getByRole("heading", { name: "Page not found" })).toBeTruthy();
    expect(
      screen.getByRole("link", { name: "Back to the console" }).getAttribute("href"),
    ).toBe("/console");
  });

  it("discloses no part of the authenticated surface", () => {
    // /console/providers, /console/feature-gates and /console/marketplace call
    // notFound() deliberately as the access control for the #947/#948/#949
    // family. A 404 rendered inside ConsoleShell would hand a non-admin the
    // whole rail, including those three entries, and so undo the gate that
    // sent them here. The only link on this page is the console front door.
    render(
      <NextIntlClientProvider locale="en" messages={enMessages}>
        <NotFound />
      </NextIntlClientProvider>,
    );

    const links = screen.getAllByRole("link");
    expect(links.map((link) => link.getAttribute("href"))).toEqual(["/console"]);
    expect(document.body.textContent ?? "").not.toMatch(
      /Providers|Feature gates|Marketplace|Sign out|Workspace/i,
    );
  });
});

describe("observability tiles", () => {
  it("never names a server-side environment variable in customer copy", () => {
    // GRAFANA_BASE_URL is unset under vitest, so the two Grafana tiles render
    // their disabled state, which is the branch that used to print the
    // variable name straight into the page.
    render(<ObservabilityTiles />);

    expect(document.body.textContent ?? "").not.toContain("GRAFANA_BASE_URL");
    expect(screen.getAllByText("Not available on this deployment.").length).toBe(2);
  });
});

describe("delta percentage display cap", () => {
  it("prints exact figures below the cap", () => {
    expect(formatDeltaPercent(12.34)).toBe("12.3%");
    expect(formatDeltaPercent(-45.67)).toBe("45.7%");
    expect(formatDeltaPercent(250)).toBe("250%");
    expect(formatDeltaPercent(999.4)).toBe("999%");
  });

  it("states a bound instead of a seven-digit percentage", () => {
    expect(formatDeltaPercent(1000)).toBe("over 1,000%");
    expect(formatDeltaPercent(3549613)).toBe("over 1,000%");
  });
});

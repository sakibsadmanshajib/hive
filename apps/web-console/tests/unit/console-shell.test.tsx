import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { NextIntlClientProvider } from "next-intl";

import { ConsoleShell } from "@/components/app-shell/console-shell";
import type { ViewerMembership } from "@/lib/control-plane/client";
import type { RoleGateViewer } from "@/lib/viewer-gates";
import enMessages from "@/messages/en.json";

// LocaleSwitcher is an async Server Component (reads next-intl/server +
// cookies indirectly); it cannot render under plain React Testing Library.
// It is unrelated to the sidebar workspace control under test here, so it is
// stubbed out rather than pulled into this unit test.
vi.mock("@/components/locale-switcher", () => ({
  LocaleSwitcher: () => null,
}));

const twoMemberships: ViewerMembership[] = [
  {
    account_id: "acct_1",
    account_display_name: "Hive Demo",
    account_slug: "hive-demo",
    display_name: "Hive Demo",
    role: "owner",
    status: "active",
  },
  {
    account_id: "acct_2",
    account_display_name: "Second Workspace",
    account_slug: "second-workspace",
    display_name: "Second Workspace",
    role: "member",
    status: "active",
  },
];

const PLAIN_MEMBER: RoleGateViewer = {
  permissions: ["analytics.view"],
  workspace_admin: false,
};
const WORKSPACE_ADMIN: RoleGateViewer = {
  permissions: ["members.invite", "members.manage", "workspace.settings"],
  workspace_admin: true,
};
// Issue #1660: a personal tenant's sole owner. Owner of their own billing
// account, so they hold the workspace permissions above, and deliberately not
// an administrator of their tenant (signup.insertPersonalMembership writes
// tenant_users.role = MEMBER), so the control-plane refuses the two surfaces
// this section offers.
const PERSONAL_TENANT_SOLE_OWNER: RoleGateViewer = {
  permissions: ["members.invite", "members.manage", "workspace.settings"],
  workspace_admin: false,
};
const PLATFORM_ADMIN: RoleGateViewer = {
  permissions: ["platform.admin"],
  workspace_admin: false,
};

function renderShell(
  memberships: ViewerMembership[],
  viewer: RoleGateViewer = WORKSPACE_ADMIN,
) {
  return render(
    <NextIntlClientProvider locale="en" messages={enMessages}>
      <ConsoleShell
        workspace={{ id: "acct_1", name: "Hive Demo", slug: "hive-demo" }}
        memberships={memberships}
        viewer={viewer}
        user={{ email: "owner@example.com" }}
        active="/console"
      >
        <div>content</div>
      </ConsoleShell>
    </NextIntlClientProvider>,
  );
}

// Regression test for issue #785: the sidebar "WORKSPACE" button carried
// aria-haspopup="menu" but had no click handler and rendered no menu at all,
// for any membership count. A signed-in user belonging to more than one
// workspace had no mouse path to switch between them.
describe("console sidebar workspace control (issue #785)", () => {
  it("lets a multi-workspace user reach a real switch control with the current workspace marked", () => {
    renderShell(twoMemberships);

    const select = screen.getByRole("combobox", {
      name: "Switch workspace",
    }) as HTMLSelectElement;
    expect(select.value).toBe("acct_1");

    expect(screen.getAllByRole("option")).toHaveLength(2);
    expect(screen.getByText("Hive Demo (current)")).toBeTruthy();
  });

  it("does not present the control as an interactive menu when the user has exactly one workspace", () => {
    renderShell([twoMemberships[0]]);

    expect(screen.queryByRole("combobox")).toBeNull();
    expect(document.querySelector("[aria-haspopup]")).toBeNull();
    expect(screen.getByText("Hive Demo")).toBeTruthy();
  });
});

// Issues #947/#948/#949 family: the rail must not advertise a link the route
// would 404 for this viewer. app/console/{providers,feature-gates,
// marketplace}/page.tsx do the actual access control (notFound() before any
// data fetch, see __tests__/console-role-gating.test.tsx); this only checks
// the rail agrees with them link-for-link so a customer never sees a dead end.
describe("console sidebar admin section role gating", () => {
  it("shows no admin nav entries and no Admin heading for a plain member", () => {
    renderShell([twoMemberships[0]], PLAIN_MEMBER);

    expect(screen.queryByRole("link", { name: /providers/i })).toBeNull();
    expect(screen.queryByRole("link", { name: /feature gates/i })).toBeNull();
    expect(screen.queryByRole("link", { name: /marketplace/i })).toBeNull();
    expect(screen.queryByText("Admin")).toBeNull();
  });

  it("shows feature gates and marketplace but not providers for a workspace administrator", () => {
    renderShell([twoMemberships[0]], WORKSPACE_ADMIN);

    expect(screen.queryByRole("link", { name: /providers/i })).toBeNull();
    expect(screen.getByRole("link", { name: /feature gates/i })).toBeTruthy();
    expect(screen.getByRole("link", { name: /marketplace/i })).toBeTruthy();
    expect(screen.getByText("Admin")).toBeTruthy();
  });

  it("offers no admin entries to a personal tenant's sole owner", () => {
    renderShell([twoMemberships[0]], PERSONAL_TENANT_SOLE_OWNER);

    expect(screen.queryByRole("link", { name: /feature gates/i })).toBeNull();
    expect(screen.queryByRole("link", { name: /marketplace/i })).toBeNull();
    expect(screen.queryByText("Admin")).toBeNull();
  });

  it("shows all three admin entries for a platform admin", () => {
    renderShell([twoMemberships[0]], PLATFORM_ADMIN);

    expect(screen.getByRole("link", { name: /providers/i })).toBeTruthy();
    expect(screen.getByRole("link", { name: /feature gates/i })).toBeTruthy();
    expect(screen.getByRole("link", { name: /marketplace/i })).toBeTruthy();
  });
});

// Regression guard for the privacy/data-policy page (issue: parity re-score
// found Hive had no privacy surface at all): the nav is the one place every
// console page shares, so a link that never got added here is a page nobody
// can reach.
describe("console sidebar privacy nav entry", () => {
  it("links to /console/privacy", () => {
    renderShell([twoMemberships[0]]);

    const link = screen.getByRole("link", { name: /privacy/i });
    expect(link.getAttribute("href")).toBe("/console/privacy");
  });
});

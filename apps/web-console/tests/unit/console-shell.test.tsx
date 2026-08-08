import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { NextIntlClientProvider } from "next-intl";

import { ConsoleShell } from "@/components/app-shell/console-shell";
import type { ViewerMembership } from "@/lib/control-plane/client";
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

function renderShell(memberships: ViewerMembership[]) {
  return render(
    <NextIntlClientProvider locale="en" messages={enMessages}>
      <ConsoleShell
        workspace={{ id: "acct_1", name: "Hive Demo", slug: "hive-demo" }}
        memberships={memberships}
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

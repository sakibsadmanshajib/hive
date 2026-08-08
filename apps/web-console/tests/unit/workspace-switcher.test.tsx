import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { fireEvent, render, screen } from "@testing-library/react";
import { NextIntlClientProvider } from "next-intl";

import { WorkspaceSwitcher } from "@/components/workspace-switcher";
import type { ViewerMembership } from "@/lib/control-plane/client";
import enMessages from "@/messages/en.json";

const workspaceSwitcherPath = resolve(
  process.cwd(),
  "components/workspace-switcher.tsx"
);

const memberships: ViewerMembership[] = [
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

function renderSwitcher() {
  return render(
    <NextIntlClientProvider locale="en" messages={enMessages}>
      <WorkspaceSwitcher
        memberships={memberships}
        currentAccountId="acct_1"
        currentAccountName="Hive Demo"
        ariaLabel="Switch workspace"
      />
    </NextIntlClientProvider>
  );
}

// Records what the control would have posted, in place of a real navigation.
// jsdom never submits, so the component's requestSubmit call is intercepted and
// the live form state snapshotted at that instant. Reading the payload rather
// than the select's value is the point: the select's value is what the test
// itself just wrote into the DOM, so asserting on it passes with no handler at
// all. Only the payload proves a submission carried the new choice out.
function captureSubmissions(form: HTMLFormElement): FormData[] {
  const submissions: FormData[] = [];
  form.requestSubmit = () => {
    submissions.push(new FormData(form));
  };
  return submissions;
}

function switcherForm(): HTMLFormElement {
  const select = screen.getByRole("combobox", { name: "Switch workspace" });
  const form = select.closest("form");
  if (!form) {
    throw new Error("workspace select is not inside a form");
  }
  return form;
}

describe("workspace switcher component boundary", () => {
  it("is marked as a client component before attaching DOM event handlers", () => {
    const source = readFileSync(workspaceSwitcherPath, "utf8");
    const firstLine = source.split("\n")[0]?.trim() ?? "";
    expect(firstLine).toBe("\"use client\";");
  });
});

// Issue #794. PR #786 shipped the onChange handler that makes this control do
// anything at all, and deleting that handler left all 423 unit tests passing:
// the only test on the component read its source text for the "use client"
// directive. A select that changes nothing looks identical to a working one in
// the DOM, so the assertion has to be on the submission the change triggers.
describe("workspace switcher submits the chosen workspace", () => {
  it("posts the newly selected workspace to the server switch route on change", () => {
    renderSwitcher();
    const form = switcherForm();
    const submissions = captureSubmissions(form);

    fireEvent.change(screen.getByRole("combobox", { name: "Switch workspace" }), {
      target: { value: "acct_2" },
    });

    expect(submissions).toHaveLength(1);
    expect(submissions[0]?.get("account_id")).toBe("acct_2");
  });

  it("submits to the server rather than holding the choice in client state", () => {
    // The switch has to round-trip through the server for it to still be in
    // effect after a reload: the account is re-read from the session cookie
    // the /console/account-switch handler writes, not from this component.
    // tests/e2e/console-workspace-switch.spec.ts proves the surviving half
    // against a running deployment; this asserts the wiring that makes it
    // possible.
    renderSwitcher();
    const form = switcherForm();

    expect(form.getAttribute("method")?.toUpperCase()).toBe("POST");
    expect(form.getAttribute("action")).toBe("/console/account-switch");
    expect(
      screen.getByRole("combobox", { name: "Switch workspace" })
    ).toHaveProperty("name", "account_id");
  });

  it("does not submit before the user changes anything", () => {
    renderSwitcher();
    const submissions = captureSubmissions(switcherForm());

    expect(submissions).toHaveLength(0);
  });
});

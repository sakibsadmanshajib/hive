import { expect, type Locator, type Page } from "@playwright/test";

// Sidebar workspace switcher (components/workspace-switcher.tsx). The control
// is a native <select> inside a POST form to /console/account-switch, so
// choosing an option performs a real navigation and the selection is persisted
// server side in the hive_account_id cookie, not in client state.

export const WORKSPACE_SELECT_LABEL = "Switch workspace";

export function workspaceSelect(page: Page): Locator {
  return page.getByRole("combobox", { name: WORKSPACE_SELECT_LABEL });
}

// accountIdFor returns the account id behind the option whose text starts with
// displayName. Option text carries a " (current)" suffix for the active
// workspace, so it is matched by prefix rather than equality.
export async function accountIdFor(
  page: Page,
  displayName: string
): Promise<string> {
  const option = workspaceSelect(page)
    .locator("option")
    .filter({ hasText: displayName })
    .first();
  await expect(option).toHaveCount(1);
  const value = await option.getAttribute("value");
  expect(value, `no account id on the option for ${displayName}`).toBeTruthy();
  return value as string;
}

// switchToWorkspace selects displayName and waits for the resulting navigation
// to settle on the console. Throws if the workspace is not in the viewer's
// membership list.
export async function switchToWorkspace(
  page: Page,
  displayName: string
): Promise<string> {
  const accountId = await accountIdFor(page, displayName);
  await Promise.all([
    page.waitForURL((url) => url.pathname.startsWith("/console"), {
      timeout: 25_000,
    }),
    workspaceSelect(page).selectOption(accountId),
  ]);
  return accountId;
}

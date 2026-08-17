import { expect, type Locator, type Page } from "@playwright/test";

// Sidebar workspace switcher (components/workspace-switcher.tsx). The control
// is a native <select> inside a POST form to /console/account-switch, so
// choosing an option performs a real navigation and the selection is persisted
// server side in the hive_account_id cookie, not in client state.

export const WORKSPACE_SELECT_LABEL = "Switch workspace";

export function workspaceSelect(page: Page): Locator {
  return page.getByRole("combobox", { name: WORKSPACE_SELECT_LABEL });
}

function escapeRegExp(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

// accountIdFor returns the account id behind the option whose full text
// equals displayName. Option text carries a " (current)" suffix for the
// active workspace, so that suffix is optional but nothing else is: matching
// is exact rather than substring, so a workspace named "Team" cannot match an
// option for "Team Archive" that happens to sort first.
export async function accountIdFor(
  page: Page,
  displayName: string
): Promise<string> {
  const options = workspaceSelect(page)
    .locator("option")
    .filter({
      hasText: new RegExp(`^${escapeRegExp(displayName)}(?: \\(current\\))?$`),
    });
  await expect(options).toHaveCount(1);
  const option = options.first();
  const value = await option.getAttribute("value");
  expect(value, `no account id on the option for ${displayName}`).toBeTruthy();
  return value as string;
}

// switchToWorkspace selects displayName and waits for the resulting navigation
// to settle on the console. Throws if the workspace is not in the viewer's
// membership list.
//
// The wait is on the replacement document, deliberately not on the URL. The
// form POSTs to /console/account-switch and that handler always 303s back to
// /console, so on the console the URL is identical before and after the
// switch. page.waitForURL() returns as soon as the CURRENT url matches the
// pattern (playwright-core Frame.waitForURL short-circuits to
// waitForLoadState) rather than waiting for a navigation, so the previous
// wait here resolved immediately every time and never observed the switch at
// all. Whatever the caller did next then raced the in-flight POST: issue #826
// saw `page.reload: net::ERR_ABORTED; maybe frame was detached?` in CI and
// `waiting for navigation to finish...` locally, and saw the budget page
// render the pre-switch workspace.
//
// A `load` event is the right signal precisely because it is per document
// rather than per URL or per response: the document that is current when this
// is called has already fired its own, so the next one belongs to the page the
// switch produced, and it fires after that document is committed. Waiting on
// the redirect's response instead is not enough: the bytes arrive before the
// browser swaps documents, and a page.goto() issued in that window still dies
// with "interrupted by another navigation".
export async function switchToWorkspace(
  page: Page,
  displayName: string
): Promise<string> {
  const accountId = await accountIdFor(page, displayName);
  const select = workspaceSelect(page);

  // Selecting the option that is already selected navigates nowhere, so there
  // would be no load event to wait for and this would hang until the timeout.
  // React's change plugin compares against the tracked value and never
  // dispatches onChange when it is unchanged, so the form is never submitted.
  // The select's value is server-rendered from the account the server
  // currently considers current, which makes it an honest answer to "are we
  // already there".
  if ((await select.inputValue()) === accountId) {
    return accountId;
  }

  // Registered before the action so the event cannot be missed.
  const switched = page.waitForEvent("load", { timeout: 25_000 });
  await select.selectOption(accountId);
  await switched;
  return accountId;
}

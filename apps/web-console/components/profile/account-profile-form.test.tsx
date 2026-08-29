/**
 * Behavioral tests for AccountProfileForm: the form at
 * /console/settings/profile, wired via a Next.js Server Action
 * (useActionState). A live authenticated sweep found two silent-save
 * defects here (2026-08-29):
 *
 * 1. This form has five `required` inputs across three cards (Owner,
 *    Account, Location). An account whose Country / State fields were
 *    never filled in (the common state for a freshly-provisioned account)
 *    has those two inputs sitting empty and `required`. Clicking Save to
 *    change only the Owner Name then hits native HTML5 constraint
 *    validation on the unrelated, untouched Country/State inputs before
 *    the `submit` event ever fires, so React's action handler never runs,
 *    no network request is sent, and the page is indistinguishable from a
 *    silently ignored click: no error, no success, no persisted change.
 *    Confirmed live: zero non-GET requests recorded in a full HAR capture
 *    of the save attempt. Fix: `noValidate` on the form, so submission
 *    always reaches the server action, which already returns per-field
 *    errors (`state.fieldErrors`) the form already renders.
 * 2. Even a genuinely successful save rendered no confirmation at all --
 *    `redirect()` inside the action just reloads the page with the
 *    server's now-current values, and nothing here ever showed a success
 *    message. Fix: the page redirects with `?saved=1`, and this form
 *    renders a `role="status"` confirmation when `justSaved` is true and
 *    the state carries no error.
 */
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";

import { AccountProfileForm } from "./account-profile-form";
import type { AccountProfileFormAction, AccountProfileFormState } from "./account-profile-form";
import type { AccountProfileFormValues } from "@/lib/profile-schemas";

function baseValues(): AccountProfileFormValues {
  return {
    ownerName: "Jane Doe",
    loginEmail: "jane@example.com",
    accountName: "Acme",
    accountType: "personal",
    countryCode: "",
    stateRegion: "",
  };
}

function cleanState(): AccountProfileFormState {
  return {
    fieldErrors: {},
    formError: null,
    values: baseValues(),
  };
}

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("AccountProfileForm behavior", () => {
  it("submits even when a pre-existing required field the user did not touch is empty", async () => {
    const action: AccountProfileFormAction = vi.fn().mockResolvedValue(cleanState());

    render(
      <AccountProfileForm
        action={action}
        initialValues={baseValues()}
        submitLabel="Save profile"
        justSaved={false}
      />,
    );

    // Country and State are empty and required (see baseValues above), and
    // this click only ever touches the submit button -- it never fills them.
    fireEvent.click(screen.getByRole("button", { name: /save profile/i }));

    await waitFor(() => {
      expect(action).toHaveBeenCalledTimes(1);
    });
  });

  it("shows a visible success confirmation once the page reports a completed save", () => {
    render(
      <AccountProfileForm
        action={vi.fn()}
        initialValues={baseValues()}
        submitLabel="Save profile"
        justSaved={true}
      />,
    );

    expect(screen.getByRole("status").textContent).toContain("saved");
  });

  it("shows no confirmation before any save has happened", () => {
    render(
      <AccountProfileForm
        action={vi.fn()}
        initialValues={baseValues()}
        submitLabel="Save profile"
        justSaved={false}
      />,
    );

    expect(screen.queryByRole("status")).toBeNull();
  });

  it("a validation failure from the server surfaces per-field errors, not a stale success banner", async () => {
    const rejected: AccountProfileFormState = {
      fieldErrors: { countryCode: "Country is required." },
      formError: "Please complete the required fields.",
      values: baseValues(),
    };
    const action: AccountProfileFormAction = vi.fn().mockResolvedValue(rejected);

    render(
      <AccountProfileForm
        action={action}
        initialValues={baseValues()}
        submitLabel="Save profile"
        justSaved={true}
      />,
    );

    fireEvent.click(screen.getByRole("button", { name: /save profile/i }));

    await waitFor(() => {
      expect(screen.getAllByRole("alert").length).toBeGreaterThan(0);
    });
    const alerts = screen.getAllByRole("alert").map((el) => el.textContent);
    expect(alerts).toContain("Please complete the required fields.");
    expect(screen.queryByRole("status")).toBeNull();
  });
});

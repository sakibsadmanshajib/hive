"use client";

import { useTranslations } from "next-intl";

import { cn } from "@/lib/cn";
import type { ViewerMembership } from "@/lib/control-plane/client";

interface WorkspaceSwitcherProps {
  memberships: ViewerMembership[];
  currentAccountId: string;
  currentAccountName: string;
  /** Accessible name for the control when it renders as interactive. */
  ariaLabel: string;
}

// Renders as a native <select> (a listbox) rather than a hand-rolled ARIA
// menu widget: the browser already gives correct keyboard navigation, focus
// handling, and screen-reader semantics for "pick one of N mutually
// exclusive options", and it POSTs straight to the existing, already
// server-validated /console/account-switch handler. See issue #785 -- the
// previous sidebar control claimed aria-haspopup="menu" but wired nothing,
// which was also the wrong ARIA pattern for a single-selection control.
export function WorkspaceSwitcher({
  memberships,
  currentAccountId,
  currentAccountName,
  ariaLabel,
}: WorkspaceSwitcherProps) {
  const t = useTranslations("WorkspaceSwitcher");

  // Memberships arrive active and invited alike. Only an accepted seat is a
  // workspace the viewer can be switched into: /console/account-switch refuses
  // an invited target, so listing one here would offer an option that silently
  // returns the viewer to the same workspace with no message.
  const switchable = memberships.filter(
    (membership) => membership.status === "active",
  );

  // Exactly one workspace: nothing to switch to, so the control must not
  // present itself as interactive at all (issue #785) -- a static label, not
  // a control that opens on an empty list.
  if (switchable.length <= 1) {
    return (
      <div
        className={cn(
          "mt-4 w-full flex items-center justify-between gap-2",
          "rounded-md border border-[var(--color-border)] bg-[var(--color-surface-inset)]",
          "px-2.5 py-2",
        )}
      >
        <span className="flex flex-col gap-0.5 min-w-0">
          <span className="text-2xs uppercase tracking-wider text-[var(--color-ink-3)]">
            {t("label")}
          </span>
          <span className="text-sm text-[var(--color-ink)] truncate">
            {currentAccountName}
          </span>
        </span>
      </div>
    );
  }

  return (
    <form
      method="POST"
      action="/console/account-switch"
      className={cn(
        "mt-4 w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface-inset)]",
        "transition-colors duration-[var(--duration-fast)] hover:border-[var(--color-border-strong)]",
      )}
    >
      <label
        htmlFor="workspace-switcher-select"
        className="block px-2.5 pt-2 text-2xs uppercase tracking-wider text-[var(--color-ink-3)]"
      >
        {t("label")}
      </label>
      <select
        id="workspace-switcher-select"
        name="account_id"
        defaultValue={currentAccountId}
        aria-label={ariaLabel}
        onChange={(event) => event.currentTarget.form?.requestSubmit()}
        className={cn(
          "w-full bg-transparent px-2.5 pb-2 pt-0.5 text-sm text-[var(--color-ink)]",
          "cursor-pointer rounded-md",
          "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-accent)]",
        )}
      >
        {switchable.map((membership) => (
          <option key={membership.account_id} value={membership.account_id}>
            {membership.account_display_name}
            {membership.account_id === currentAccountId ? ` (${t("current")})` : ""}
          </option>
        ))}
      </select>
    </form>
  );
}

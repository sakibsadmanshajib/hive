import { getLocale, getTranslations } from "next-intl/server";

import { cn } from "@/lib/cn";
import { LOCALES, LOCALE_COOKIE, LOCALE_LABELS } from "@/lib/i18n/locales";

interface LocaleSwitcherProps {
  /** Path to come back to, so switching language keeps the current page. */
  returnTo?: string;
  className?: string;
}

// A plain form POST to /console/locale — no client component, no onChange
// handler, and it works with JavaScript disabled. The route replies with a 303
// so the page reloads carrying the new cookie; see the note in route.ts for
// why a Server Action does not work here.
export async function LocaleSwitcher({
  returnTo = "/console",
  className,
}: LocaleSwitcherProps) {
  const [active, t] = await Promise.all([
    getLocale(),
    getTranslations("LocaleSwitcher"),
  ]);

  return (
    <form
      method="POST"
      action="/console/locale"
      aria-label={t("label")}
      className={cn(
        "flex items-center overflow-hidden rounded-md",
        "border border-[var(--color-border)]",
        className,
      )}
    >
      <input type="hidden" name="return_to" value={returnTo} />
      {LOCALES.map((locale) => {
        const isActive = locale === active;
        return (
          <button
            key={locale}
            type="submit"
            name={LOCALE_COOKIE}
            value={locale}
            aria-current={isActive ? "true" : undefined}
            aria-label={t("switchTo", { language: LOCALE_LABELS[locale] })}
            className={cn(
              "px-2 py-1 text-2xs leading-none",
              "transition-colors duration-[var(--duration-fast)]",
              "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-accent)]",
              isActive
                ? "bg-[var(--color-surface-inset)] font-medium text-[var(--color-ink)]"
                : "text-[var(--color-ink-3)] hover:text-[var(--color-ink)]",
            )}
          >
            {LOCALE_LABELS[locale]}
          </button>
        );
      })}
    </form>
  );
}

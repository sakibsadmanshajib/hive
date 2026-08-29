"use client";

import * as React from "react";
import { Menu, X } from "lucide-react";

import { cn } from "@/lib/cn";

interface ConsoleFrameProps {
  /** Sidebar body: brand, workspace switcher, primary nav, account footer. */
  sidebar: React.ReactNode;
  /** Left-hand topbar slot (page label / breadcrumb). */
  topbar?: React.ReactNode;
  /** Right-hand header slot (docs link, locale switcher, sign out). */
  headerActions: React.ReactNode;
  children: React.ReactNode;
  openNavLabel: string;
  closeNavLabel: string;
}

const SIDEBAR_ID = "console-primary-nav";

// Tailwind's `lg`. Duplicated here because the drawer's open state is JS and
// its layout is CSS, and the two have to agree on where the rail stops being a
// drawer. Change both or neither.
const LG_BREAKPOINT_PX = 1024;

/**
 * Layout frame for the console, and the only piece of the shell that needs
 * client state: whether the sidebar is showing as a drawer.
 *
 * Below `lg` the sidebar used to be `hidden` with nothing replacing it, so a
 * phone, a portrait tablet and a small laptop had no route to Billing,
 * Members, Analytics, Logs, API keys or Settings from any page (issue #1367).
 * The same sidebar element is now the drawer, rather than a second copy of
 * the nav rendered for small screens: one element means one set of links, one
 * set of element ids, and no way for the two to drift apart.
 *
 * Closed, the sidebar stays `hidden`, which keeps it out of the tab order
 * instead of parking thirteen off-screen links in it. Open, it is a fixed
 * overlay above a scrim; `lg:` overrides return it to its static grid column
 * at desktop widths regardless of the open flag, so resizing a phone-width
 * window up never strands the drawer on top of the page.
 */
export function ConsoleFrame({
  sidebar,
  topbar,
  headerActions,
  children,
  openNavLabel,
  closeNavLabel,
}: ConsoleFrameProps) {
  const [open, setOpen] = React.useState(false);
  const sidebarRef = React.useRef<HTMLElement>(null);
  const toggleRef = React.useRef<HTMLButtonElement>(null);

  React.useEffect(() => {
    if (!open) {
      return;
    }
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        setOpen(false);
      }
    };
    document.addEventListener("keydown", onKeyDown);

    // The drawer sits over the page, so the page must not scroll behind it.
    const previousOverflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";

    // Crossing up to lg turns the drawer back into the static rail, and a
    // stale `open` would then leave the body scroll locked, <main> inert, and
    // Escape mutating state that no longer means anything on this layout.
    // A resize listener rather than matchMedia: same answer, one less browser
    // API, and it is bound only while the drawer is open, which is a rare and
    // short-lived state.
    const onResize = () => {
      if (window.innerWidth >= LG_BREAKPOINT_PX) {
        setOpen(false);
      }
    };
    window.addEventListener("resize", onResize);

    return () => {
      document.removeEventListener("keydown", onKeyDown);
      window.removeEventListener("resize", onResize);
      document.body.style.overflow = previousOverflow;
    };
  }, [open]);

  // Move focus into the drawer when it opens and hand it back to the toggle
  // when it closes, so a keyboard or screen-reader user is not left with focus
  // on a control that is now behind a scrim, or dumped at the top of the
  // document. The panel itself takes focus rather than its first link: the
  // reading order then starts at the workspace name, as it does visually.
  React.useEffect(() => {
    if (open) {
      sidebarRef.current?.focus();
    } else if (
      toggleRef.current &&
      document.activeElement instanceof HTMLElement &&
      sidebarRef.current?.contains(document.activeElement)
    ) {
      toggleRef.current.focus();
    }
  }, [open]);

  // Following any link inside the drawer navigates away, so the drawer has to
  // close with it or it covers the page it just opened. Keyed on an actual
  // anchor rather than on the route changing: tapping the entry for the page
  // you are already on changes no route and still has to dismiss.
  const closeIfNavigating = (event: React.MouseEvent<HTMLElement>) => {
    if (open && (event.target as HTMLElement).closest("a")) {
      setOpen(false);
    }
  };

  return (
    <div className="min-h-screen grid grid-cols-1 lg:grid-cols-[240px_1fr] bg-[var(--color-canvas)]">
      {open ? (
        <div
          aria-hidden="true"
          onClick={() => setOpen(false)}
          className="fixed inset-0 z-40 bg-black/50 lg:hidden"
        />
      ) : null}

      <aside
        id={SIDEBAR_ID}
        ref={sidebarRef}
        tabIndex={open ? -1 : undefined}
        onClick={closeIfNavigating}
        className={cn(
          "focus-visible:outline-none",
          "flex-col border-r border-[var(--color-border)] bg-[var(--color-surface)]",
          open
            ? "flex fixed inset-y-0 left-0 z-50 w-[17rem] shadow-[var(--shadow-lg)] lg:static lg:z-auto lg:w-auto lg:shadow-none"
            : "hidden lg:flex",
        )}
      >
        {sidebar}
      </aside>

      <div className="flex flex-col min-w-0">
        <header
          className={cn(
            "h-14 shrink-0 flex items-center justify-between gap-4",
            "border-b border-[var(--color-border)] bg-[var(--color-surface)]",
            "px-4 sm:px-6",
          )}
        >
          <div className="flex items-center gap-3 min-w-0 text-sm text-[var(--color-ink-2)]">
            <button
              type="button"
              ref={toggleRef}
              onClick={() => setOpen((value) => !value)}
              aria-expanded={open}
              aria-controls={SIDEBAR_ID}
              aria-label={open ? closeNavLabel : openNavLabel}
              className={cn(
                "lg:hidden shrink-0 h-11 w-11 -ml-2 grid place-items-center rounded-md",
                "text-[var(--color-ink-2)]",
                "transition-colors duration-[var(--duration-fast)]",
                "hover:bg-[var(--color-surface-inset)] hover:text-[var(--color-ink)]",
                "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-accent)]",
              )}
            >
              {open ? <X size={18} /> : <Menu size={18} />}
            </button>
            <span className="min-w-0 truncate">{topbar}</span>
          </div>
          <div className="flex items-center gap-3 shrink-0">{headerActions}</div>
        </header>
        {/*
          `inert` while the drawer is open: the scrim stops the mouse, but
          without this a keyboard user tabbing past the last drawer entry
          walked straight into page content sitting behind it, and a screen
          reader read the whole page underneath. Applied to <main> only, not
          to the header, so the toggle stays reachable as the close control
          (a drawer whose only exits were Escape and a scrim click would be
          worse than the tab leak).
        */}
        <main className="flex-1 overflow-y-auto" inert={open}>
          <div className="mx-auto w-full max-w-6xl px-4 py-8 sm:px-6">
            {children}
          </div>
        </main>
      </div>
    </div>
  );
}

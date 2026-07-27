import * as React from "react";
import Link from "next/link";
import {
  LayoutGrid,
  KeyRound,
  CreditCard,
  BarChart3,
  Boxes,
  Users,
  Settings,
  Wallet,
  ToggleRight,
  Store,
  LogOut,
} from "lucide-react";

import { useTranslations } from "next-intl";

import { cn } from "@/lib/cn";
import { HiveMark } from "@/components/brand/hive-mark";
import { LocaleSwitcher } from "@/components/locale-switcher";

// Labels are message keys resolved at render, not literals — the nav is the
// one place every console page shares, so it has to follow the active locale.
const NAV_GROUPS: ReadonlyArray<{
  labelKey: string;
  items: ReadonlyArray<{ href: string; labelKey: string; icon: React.ReactNode }>;
}> = [
  {
    labelKey: "groupBuild",
    items: [
      { href: "/console", labelKey: "overview", icon: <LayoutGrid size={14} /> },
      { href: "/console/api-keys", labelKey: "apiKeys", icon: <KeyRound size={14} /> },
      { href: "/console/catalog", labelKey: "catalog", icon: <Boxes size={14} /> },
      { href: "/console/analytics", labelKey: "analytics", icon: <BarChart3 size={14} /> },
    ],
  },
  {
    labelKey: "groupWorkspace",
    items: [
      { href: "/console/billing", labelKey: "billing", icon: <Wallet size={14} /> },
      { href: "/console/members", labelKey: "members", icon: <Users size={14} /> },
      { href: "/console/settings/profile", labelKey: "settings", icon: <Settings size={14} /> },
    ],
  },
  {
    labelKey: "groupAdmin",
    items: [
      {
        href: "/console/feature-gates",
        labelKey: "featureGates",
        icon: <ToggleRight size={14} />,
      },
      {
        href: "/console/marketplace",
        labelKey: "marketplace",
        icon: <Store size={14} />,
      },
    ],
  },
];

interface ConsoleShellProps {
  workspace: {
    name: string;
    slug?: string;
  };
  user: {
    email: string;
    name?: string | null;
  };
  topbar?: React.ReactNode;
  children: React.ReactNode;
  active?: string;
}

export function ConsoleShell({
  workspace,
  user,
  topbar,
  children,
  active,
}: ConsoleShellProps) {
  const tNav = useTranslations("Nav");
  const tShell = useTranslations("Shell");

  return (
    <div className="min-h-screen grid grid-cols-1 lg:grid-cols-[240px_1fr] bg-[var(--color-canvas)]">
      <aside className="hidden lg:flex flex-col border-r border-[var(--color-border)] bg-[var(--color-surface)]">
        <div className="px-5 py-5 border-b border-[var(--color-border)]">
          <Link
            href="/console"
            className="flex items-center gap-2.5 text-[var(--color-ink)] focus-visible:outline-none"
          >
            <HiveMark size={22} />
            <span className="font-display text-base font-semibold leading-none tracking-[-0.03em]">
              Hive
            </span>
          </Link>
          <button
            type="button"
            className={cn(
              "mt-4 w-full flex items-center justify-between gap-2",
              "rounded-md border border-[var(--color-border)] bg-[var(--color-surface-inset)]",
              "px-2.5 py-2 text-left",
              "transition-colors duration-[var(--duration-fast)] hover:border-[var(--color-border-strong)]",
            )}
            aria-haspopup="menu"
            aria-label={tShell("workspaceMenu")}
          >
            <span className="flex flex-col gap-0.5 min-w-0">
              <span className="text-2xs uppercase tracking-wider text-[var(--color-ink-3)]">
                {tShell("workspace")}
              </span>
              <span className="text-sm text-[var(--color-ink)] truncate">
                {workspace.name}
              </span>
            </span>
            <ChevronGlyph />
          </button>
        </div>
        <nav
          aria-label={tShell("primaryNav")}
          className="flex-1 overflow-y-auto px-3 py-4 flex flex-col gap-5"
        >
          {NAV_GROUPS.map((group) => (
            <div key={group.labelKey} className="flex flex-col gap-1">
              <span className="px-2 text-2xs uppercase tracking-wider text-[var(--color-ink-3)]">
                {tNav(group.labelKey)}
              </span>
              <ul className="flex flex-col gap-0.5">
                {group.items.map((item) => {
                  // Special-case "/console" so the dashboard root only
                  // lights up on an exact match (otherwise its prefix
                  // would mark Overview active for every nested route).
                  // Settings' href is a sub-route — broaden the match
                  // to its parent path so /console/settings/billing
                  // also activates the Settings nav item.
                  const matchPrefix =
                    item.href === "/console/settings/profile"
                      ? "/console/settings"
                      : item.href;
                  const isActive =
                    item.href === "/console"
                      ? active === "/console"
                      : active === item.href ||
                        (active?.startsWith(matchPrefix + "/") ?? false);
                  return (
                    <li key={item.href}>
                      <Link
                        href={item.href}
                        className={cn(
                          "flex items-center gap-2 rounded-md px-2 py-1.5",
                          "text-sm transition-colors duration-[var(--duration-fast)]",
                          isActive
                            ? "bg-[var(--color-surface-inset)] text-[var(--color-ink)] font-medium"
                            : "text-[var(--color-ink-2)] hover:bg-[var(--color-surface-inset)] hover:text-[var(--color-ink)]",
                        )}
                      >
                        <span
                          className={cn(
                            "shrink-0",
                            isActive
                              ? "text-[var(--color-accent)]"
                              : "text-[var(--color-ink-3)]",
                          )}
                        >
                          {item.icon}
                        </span>
                        <span className="truncate">{tNav(item.labelKey)}</span>
                      </Link>
                    </li>
                  );
                })}
              </ul>
            </div>
          ))}
        </nav>
        <div className="border-t border-[var(--color-border)] px-3 py-3">
          <div className="flex items-center gap-2 px-2 py-1.5 rounded-md">
            <div className="h-7 w-7 rounded-full bg-[var(--color-accent-soft)] grid place-items-center text-2xs text-[var(--color-accent-ink)] font-semibold">
              {(user.name?.[0] ?? user.email[0] ?? "?").toUpperCase()}
            </div>
            <div className="flex flex-col gap-0.5 min-w-0 flex-1">
              <span className="text-xs text-[var(--color-ink)] truncate">
                {user.name ?? user.email}
              </span>
              <span className="text-2xs text-[var(--color-ink-3)] truncate">
                {user.email}
              </span>
            </div>
            <SignOutButton className="shrink-0" />
          </div>
        </div>
      </aside>

      <div className="flex flex-col min-w-0">
        <header
          className={cn(
            "h-14 shrink-0 flex items-center justify-between gap-4",
            "border-b border-[var(--color-border)] bg-[var(--color-surface)]",
            "px-6",
          )}
        >
          <div className="flex items-center gap-3 text-sm text-[var(--color-ink-2)]">
            {topbar}
          </div>
          <div className="flex items-center gap-3">
            <Link
              href="https://hivegpt.io"
              className="text-xs text-[var(--color-ink-3)] hover:text-[var(--color-ink)] transition-colors"
            >
              {tShell("docs")}
            </Link>
            <LocaleSwitcher returnTo={active} />
            <SignOutButton className="lg:hidden" />
          </div>
        </header>
        <main className="flex-1 overflow-y-auto">
          <div className="mx-auto w-full max-w-6xl px-6 py-8">{children}</div>
        </main>
      </div>
    </div>
  );
}

function SignOutButton({ className }: { className?: string }) {
  const label = useTranslations("Shell")("signOut");

  return (
    <form action="/auth/sign-out" method="post" className={className}>
      <button
        type="submit"
        aria-label={label}
        title={label}
        className={cn(
          "h-7 w-7 grid place-items-center rounded-md",
          "text-[var(--color-ink-3)]",
          "transition-colors duration-[var(--duration-fast)]",
          "hover:text-[var(--color-ink)] hover:bg-[var(--color-surface-inset)]",
          "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-accent)]",
        )}
      >
        <LogOut size={14} />
      </button>
    </form>
  );
}

function ChevronGlyph() {
  return (
    <svg
      width="12"
      height="12"
      viewBox="0 0 12 12"
      fill="none"
      xmlns="http://www.w3.org/2000/svg"
      aria-hidden="true"
      className="shrink-0 text-[var(--color-ink-3)]"
    >
      <path
        d="M3 4.5l3 3 3-3M3 7.5l3-3 3 3"
        stroke="currentColor"
        strokeWidth="1.25"
        strokeLinecap="round"
        strokeLinejoin="round"
        opacity="0.6"
      />
    </svg>
  );
}

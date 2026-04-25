import * as React from "react";

import { cn } from "@/lib/cn";

interface AuthShellProps {
  eyebrow?: React.ReactNode;
  title: React.ReactNode;
  subtitle?: React.ReactNode;
  footer?: React.ReactNode;
  children: React.ReactNode;
  className?: string;
}

export function AuthShell({
  eyebrow,
  title,
  subtitle,
  footer,
  children,
  className,
}: AuthShellProps) {
  return (
    <main
      className={cn(
        "min-h-screen w-full flex flex-col",
        "bg-[var(--color-canvas)]",
        className,
      )}
    >
      <div className="flex-1 flex flex-col items-center justify-center px-6 py-16">
        <div className="w-full max-w-[380px] flex flex-col gap-7">
          <div className="flex flex-col gap-3">
            <Wordmark />
            {eyebrow ? (
              <span className="text-2xs font-medium uppercase tracking-[0.14em] text-[var(--color-ink-3)]">
                {eyebrow}
              </span>
            ) : null}
            <h1 className="text-2xl text-[var(--color-ink)] text-balance leading-tight">
              {title}
            </h1>
            {subtitle ? (
              <p className="text-sm text-[var(--color-ink-3)] leading-relaxed">
                {subtitle}
              </p>
            ) : null}
          </div>
          <div className="flex flex-col gap-5">{children}</div>
          {footer ? (
            <div className="text-xs text-[var(--color-ink-3)]">{footer}</div>
          ) : null}
        </div>
      </div>
      <footer className="border-t border-[var(--color-border)] px-6 py-4 flex items-center justify-between text-2xs text-[var(--color-ink-3)]">
        <span>&copy; {new Date().getFullYear()} Hive</span>
        <span className="font-mono tabular-nums">api.hive · v1</span>
      </footer>
    </main>
  );
}

function Wordmark() {
  return (
    <div className="flex items-center gap-2 text-[var(--color-ink)]">
      <div
        aria-hidden="true"
        className={cn(
          "h-7 w-7 rounded-md grid place-items-center",
          "bg-[var(--color-ink)] text-[var(--color-canvas)]",
          "font-display text-base leading-none",
        )}
      >
        h
      </div>
      <span className="font-display text-lg leading-none tracking-tight">
        Hive
      </span>
    </div>
  );
}

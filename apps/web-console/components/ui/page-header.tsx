import * as React from "react";

import { cn } from "@/lib/cn";

interface PageHeaderProps {
  eyebrow?: React.ReactNode;
  title: React.ReactNode;
  description?: React.ReactNode;
  actions?: React.ReactNode;
  className?: string;
}

export function PageHeader({
  eyebrow,
  title,
  description,
  actions,
  className,
}: PageHeaderProps) {
  return (
    <header
      className={cn(
        "flex flex-col gap-3 pb-6 sm:flex-row sm:items-end sm:justify-between",
        "border-b border-[var(--color-border)] mb-8",
        className,
      )}
    >
      <div className="flex flex-col gap-2 max-w-2xl">
        {eyebrow ? (
          <span className="text-2xs font-medium uppercase tracking-[0.14em] text-[var(--color-ink-3)]">
            {eyebrow}
          </span>
        ) : null}
        <h1 className="text-3xl text-[var(--color-ink)] text-balance">
          {title}
        </h1>
        {description ? (
          <p className="text-sm text-[var(--color-ink-3)] text-pretty leading-relaxed">
            {description}
          </p>
        ) : null}
      </div>
      {actions ? (
        <div className="flex items-center gap-2 shrink-0">{actions}</div>
      ) : null}
    </header>
  );
}

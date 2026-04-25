import * as React from "react";

import { cn } from "@/lib/cn";

interface EmptyStateProps {
  icon?: React.ReactNode;
  title: React.ReactNode;
  description?: React.ReactNode;
  action?: React.ReactNode;
  className?: string;
}

export function EmptyState({
  icon,
  title,
  description,
  action,
  className,
}: EmptyStateProps) {
  return (
    <div
      className={cn(
        "flex flex-col items-center justify-center text-center",
        "rounded-lg border border-dashed border-[var(--color-border)]",
        "bg-[var(--color-surface)] px-6 py-12 gap-2",
        className,
      )}
    >
      {icon ? (
        <div className="mb-1 text-[var(--color-ink-3)]">{icon}</div>
      ) : null}
      <p className="text-sm font-semibold text-[var(--color-ink)]">{title}</p>
      {description ? (
        <p className="max-w-sm text-xs text-[var(--color-ink-3)] leading-relaxed">
          {description}
        </p>
      ) : null}
      {action ? <div className="mt-3">{action}</div> : null}
    </div>
  );
}

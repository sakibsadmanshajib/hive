import * as React from "react";
import { cva, type VariantProps } from "class-variance-authority";

import { cn } from "@/lib/cn";

const badgeVariants = cva(
  [
    "inline-flex items-center gap-1 rounded-full",
    "px-2 py-0.5 text-2xs font-medium tracking-wide",
    "tabular-nums leading-none",
    "border",
  ],
  {
    variants: {
      tone: {
        neutral: [
          "bg-[var(--color-surface-inset)] border-[var(--color-border)]",
          "text-[var(--color-ink-2)]",
        ],
        accent: [
          "bg-[var(--color-accent-soft)] border-transparent",
          "text-[var(--color-accent-ink)]",
        ],
        success: [
          "bg-[var(--color-success-soft)] border-transparent",
          "text-[var(--color-success)]",
        ],
        warning: [
          "bg-[var(--color-warning-soft)] border-transparent",
          "text-[var(--color-warning)]",
        ],
        danger: [
          "bg-[var(--color-danger-soft)] border-transparent",
          "text-[var(--color-danger)]",
        ],
        outline: [
          "bg-transparent border-[var(--color-border-strong)]",
          "text-[var(--color-ink-2)]",
        ],
      },
    },
    defaultVariants: {
      tone: "neutral",
    },
  },
);

export interface BadgeProps
  extends React.HTMLAttributes<HTMLSpanElement>,
    VariantProps<typeof badgeVariants> {}

export function Badge({ className, tone, ...props }: BadgeProps) {
  return (
    <span className={cn(badgeVariants({ tone }), className)} {...props} />
  );
}

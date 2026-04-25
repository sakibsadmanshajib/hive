import * as React from "react";
import { cva, type VariantProps } from "class-variance-authority";

import { cn } from "@/lib/cn";

const buttonVariants = cva(
  [
    "inline-flex items-center justify-center gap-2 whitespace-nowrap",
    "font-medium select-none",
    "rounded-md transition-[background,color,border,box-shadow,transform]",
    "duration-[var(--duration-fast)] ease-[var(--ease-out-expo)]",
    "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-accent)] focus-visible:ring-offset-2 focus-visible:ring-offset-[var(--color-canvas)]",
    "disabled:opacity-50 disabled:pointer-events-none",
    "active:translate-y-px",
  ],
  {
    variants: {
      variant: {
        primary: [
          "bg-[var(--color-ink)] text-[var(--color-canvas)]",
          "hover:bg-[var(--color-ink-2)]",
          "shadow-[var(--shadow-xs)]",
        ],
        accent: [
          "bg-[var(--color-accent)] text-white",
          "hover:bg-[var(--color-accent-hover)]",
          "shadow-[var(--shadow-xs)]",
        ],
        secondary: [
          "bg-[var(--color-surface)] text-[var(--color-ink)]",
          "border border-[var(--color-border)]",
          "hover:bg-[var(--color-surface-2)] hover:border-[var(--color-border-strong)]",
        ],
        ghost: [
          "bg-transparent text-[var(--color-ink-2)]",
          "hover:bg-[var(--color-surface-2)] hover:text-[var(--color-ink)]",
        ],
        danger: [
          "bg-[var(--color-danger)] text-white",
          "hover:brightness-110",
        ],
        link: [
          "bg-transparent p-0 h-auto text-[var(--color-accent)]",
          "underline-offset-4 hover:underline",
        ],
      },
      size: {
        sm: "h-8 px-3 text-xs",
        md: "h-9 px-3.5 text-sm",
        lg: "h-10 px-4 text-sm",
        icon: "h-9 w-9 p-0",
      },
    },
    defaultVariants: {
      variant: "primary",
      size: "md",
    },
  },
);

export interface ButtonProps
  extends React.ButtonHTMLAttributes<HTMLButtonElement>,
    VariantProps<typeof buttonVariants> {}

export const Button = React.forwardRef<HTMLButtonElement, ButtonProps>(
  ({ className, variant, size, ...props }, ref) => (
    <button
      ref={ref}
      className={cn(buttonVariants({ variant, size }), className)}
      {...props}
    />
  ),
);
Button.displayName = "Button";

export { buttonVariants };

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
        // Label is --color-canvas, not white: the dark theme's accent is a
        // light sienna, so white-on-accent measured 2.61:1 there and 4.45:1
        // in light, both under AA on the console's primary revenue control.
        // See the --color-accent-solid block in globals.css.
        accent: [
          "bg-[var(--color-accent-solid)] text-[var(--color-canvas)]",
          "hover:bg-[var(--color-accent-solid-hover)]",
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
        // Label is --color-canvas, not white: dark mode's danger token is
        // lightened for text-on-canvas AA (issue #491), and white-on-danger
        // measured 2.67:1 there, under AA on this destructive-action control.
        // Same pairing the accent variant above already uses; see the
        // --color-danger dark override in globals.css for the measurements.
        danger: [
          "bg-[var(--color-danger)] text-[var(--color-canvas)]",
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
  ({ className, variant, size, type, ...props }, ref) => (
    // Default `type` to "button" so a Button rendered inside a <form>
    // never accidentally submits when the consumer forgot to set it.
    <button
      ref={ref}
      type={type ?? "button"}
      className={cn(buttonVariants({ variant, size }), className)}
      {...props}
    />
  ),
);
Button.displayName = "Button";

export { buttonVariants };

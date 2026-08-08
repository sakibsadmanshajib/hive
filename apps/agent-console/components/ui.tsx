import type { ReactNode } from "react";

/*
 * The handful of console primitives this app actually uses, expressed as class
 * strings rather than components.
 *
 * apps/web-console has real primitives (components/ui/button.tsx,
 * components/ui/input.tsx) built on class-variance-authority. Those are not
 * imported here for the same reason the mark is duplicated: Dockerfile.agent-
 * console copies only apps/agent-console into the image. Rather than add cva
 * and a component layer to a three-screen sidecar, the same visual result is
 * spelled out below with the same tokens. Keep the values in step with the
 * console's variants if either side changes.
 */

export const INPUT_CLASS = [
  "flex h-9 w-full rounded-md border border-[var(--color-border)]",
  "bg-[var(--color-surface)] px-3 text-sm text-[var(--color-ink)]",
  "placeholder:text-[var(--color-ink-4)]",
  "transition-[border,box-shadow] duration-[var(--duration-fast)] ease-[var(--ease-out-expo)]",
  "focus-visible:outline-none focus-visible:border-[var(--color-accent)]",
  "focus-visible:ring-4 focus-visible:ring-[var(--color-accent-soft)]",
  "disabled:cursor-not-allowed disabled:opacity-50",
].join(" ");

/*
 * The composer's prompt field. Not INPUT_CLASS with a height override: this
 * is the one element on the workspace that carries the user's own words, so
 * it steps up a size (--text-lg against the app's --text-base body) and gets
 * relaxed leading. Everything else -- border, focus ring, disabled -- stays
 * identical to INPUT_CLASS so the two read as one family. `field-sizing:
 * content` grows the box with the text where supported and is inert
 * elsewhere, which is why `rows` is still set by the caller.
 */
export const TEXTAREA_CLASS = [
  "w-full resize-y rounded-md border border-[var(--color-border)]",
  "bg-[var(--color-surface)] px-3.5 py-3",
  "text-lg leading-relaxed text-[var(--color-ink)] [field-sizing:content]",
  "placeholder:text-[var(--color-ink-3)]",
  "transition-[border,box-shadow] duration-[var(--duration-fast)] ease-[var(--ease-out-expo)]",
  "focus-visible:outline-none focus-visible:border-[var(--color-accent)]",
  "focus-visible:ring-4 focus-visible:ring-[var(--color-accent-soft)]",
  "aria-[invalid=true]:border-[var(--color-danger)]",
  "disabled:cursor-not-allowed disabled:opacity-50",
].join(" ");

const BUTTON_BASE = [
  "inline-flex items-center justify-center gap-2 whitespace-nowrap",
  "select-none rounded-md font-medium",
  "transition-[background,color,border,box-shadow,transform]",
  "duration-[var(--duration-fast)] ease-[var(--ease-out-expo)]",
  "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-accent)]",
  "focus-visible:ring-offset-2 focus-visible:ring-offset-[var(--color-canvas)]",
  "disabled:pointer-events-none disabled:opacity-50",
  "active:translate-y-px",
].join(" ");

const BUTTON_VARIANTS = {
  accent: [
    /*
     * The label flips with the palette because the accent itself does. In
     * light, --color-accent is a deep sienna and white sits on it at 4.5:1.
     * In dark it lightens to oklch(0.72 ...), where white measures 2.6:1 and
     * fails AA outright; --color-canvas is the dark ground there and gives
     * about 6.9:1. Measured in Chrome against the running app, both modes.
     */
    "bg-[var(--color-accent)] text-white dark:text-[var(--color-canvas)]",
    "hover:bg-[var(--color-accent-hover)]",
    "shadow-[var(--shadow-xs)]",
  ].join(" "),
  secondary: [
    "bg-[var(--color-surface)] text-[var(--color-ink)]",
    "border border-[var(--color-border)]",
    "hover:bg-[var(--color-surface-2)] hover:border-[var(--color-border-strong)]",
  ].join(" "),
  ghost: [
    "bg-transparent text-[var(--color-ink-2)]",
    "hover:bg-[var(--color-surface-2)] hover:text-[var(--color-ink)]",
  ].join(" "),
} as const;

const BUTTON_SIZES = {
  sm: "h-8 px-3 text-xs",
  md: "h-9 px-3.5 text-sm",
  lg: "h-10 px-4 text-sm",
} as const;

export function buttonClass(
  variant: keyof typeof BUTTON_VARIANTS = "accent",
  size: keyof typeof BUTTON_SIZES = "md",
): string {
  return `${BUTTON_BASE} ${BUTTON_VARIANTS[variant]} ${BUTTON_SIZES[size]}`;
}

export function Field({
  label,
  htmlFor,
  required,
  children,
}: {
  label: string;
  htmlFor: string;
  required?: boolean;
  children: ReactNode;
}) {
  // The required marker sits outside <label>, not inside it as the console's
  // Field does, so the field's accessible name stays exactly "Email" rather
  // than "Email *". Same visual result, cleaner name for screen readers.
  return (
    <div className="flex flex-col gap-1.5">
      <span className="flex items-center gap-1 text-xs font-medium leading-none tracking-tight text-[var(--color-ink-2)]">
        <label htmlFor={htmlFor}>{label}</label>
        {required ? (
          <span aria-hidden="true" className="text-[var(--color-accent)]">
            *
          </span>
        ) : null}
      </span>
      {children}
    </div>
  );
}

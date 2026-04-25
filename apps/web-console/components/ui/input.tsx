import * as React from "react";

import { cn } from "@/lib/cn";

export interface InputProps
  extends React.InputHTMLAttributes<HTMLInputElement> {}

export const Input = React.forwardRef<HTMLInputElement, InputProps>(
  ({ className, type = "text", ...props }, ref) => (
    <input
      ref={ref}
      type={type}
      className={cn(
        "flex h-9 w-full rounded-md border border-[var(--color-border)]",
        "bg-[var(--color-surface)] px-3 text-sm text-[var(--color-ink)]",
        "placeholder:text-[var(--color-ink-4)]",
        "transition-[border,box-shadow] duration-[var(--duration-fast)] ease-[var(--ease-out-expo)]",
        "focus-visible:outline-none focus-visible:border-[var(--color-accent)]",
        "focus-visible:ring-4 focus-visible:ring-[var(--color-accent-soft)]",
        "disabled:cursor-not-allowed disabled:opacity-50",
        "file:border-0 file:bg-transparent file:text-sm file:font-medium",
        className,
      )}
      {...props}
    />
  ),
);
Input.displayName = "Input";

export interface LabelProps
  extends React.LabelHTMLAttributes<HTMLLabelElement> {}

export const Label = React.forwardRef<HTMLLabelElement, LabelProps>(
  ({ className, ...props }, ref) => (
    <label
      ref={ref}
      className={cn(
        "text-xs font-medium text-[var(--color-ink-2)]",
        "leading-none tracking-tight",
        className,
      )}
      {...props}
    />
  ),
);
Label.displayName = "Label";

export interface FieldProps {
  label?: React.ReactNode;
  hint?: React.ReactNode;
  error?: React.ReactNode;
  required?: boolean;
  htmlFor?: string;
  children: React.ReactNode;
  className?: string;
}

export function Field({
  label,
  hint,
  error,
  required,
  htmlFor,
  children,
  className,
}: FieldProps) {
  return (
    <div className={cn("flex flex-col gap-1.5", className)}>
      {label ? (
        <Label htmlFor={htmlFor}>
          {label}
          {required ? (
            <span
              aria-hidden="true"
              className="ml-1 text-[var(--color-accent)]"
            >
              *
            </span>
          ) : null}
        </Label>
      ) : null}
      {children}
      {error ? (
        <p
          role="alert"
          className="text-xs text-[var(--color-danger)] leading-tight"
        >
          {error}
        </p>
      ) : hint ? (
        <p className="text-xs text-[var(--color-ink-3)] leading-tight">
          {hint}
        </p>
      ) : null}
    </div>
  );
}

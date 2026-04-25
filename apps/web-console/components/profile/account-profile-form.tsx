"use client";

import { useActionState } from "react";

import type {
  AccountProfileFieldErrors,
  AccountProfileFormValues,
} from "@/lib/profile-schemas";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Field, Input } from "@/components/ui/input";
import { cn } from "@/lib/cn";

export interface AccountProfileFormState {
  fieldErrors: AccountProfileFieldErrors;
  formError: string | null;
  values: AccountProfileFormValues;
}

export type AccountProfileFormAction = (
  state: AccountProfileFormState,
  formData: FormData,
) => Promise<AccountProfileFormState>;

interface AccountProfileFormProps {
  action: AccountProfileFormAction;
  initialValues: AccountProfileFormValues;
  submitLabel: string;
}

const emptyErrors: AccountProfileFieldErrors = {};

const SELECT_CLASSES = cn(
  "flex h-9 w-full rounded-md border border-[var(--color-border)]",
  "bg-[var(--color-surface)] px-3 text-sm text-[var(--color-ink)]",
  "transition-[border,box-shadow] duration-[var(--duration-fast)]",
  "ease-[var(--ease-out-expo)]",
  "focus-visible:outline-none focus-visible:border-[var(--color-accent)]",
  "focus-visible:ring-4 focus-visible:ring-[var(--color-accent-soft)]",
);

export function AccountProfileForm({
  action,
  initialValues,
  submitLabel,
}: AccountProfileFormProps) {
  const [state, formAction, isPending] = useActionState(action, {
    fieldErrors: emptyErrors,
    formError: null,
    values: initialValues,
  });

  const values = state.values;

  return (
    <form action={formAction} className="grid gap-6">
      <input
        type="hidden"
        name="loginEmail"
        value={values.loginEmail}
        readOnly
      />

      <Card>
        <CardHeader>
          <CardTitle>Owner</CardTitle>
          <CardDescription>
            The person responsible for this workspace. We use these details on
            invoices, account verification and admin notifications.
          </CardDescription>
        </CardHeader>
        <CardContent className="grid gap-4 px-5 py-5">
          <Field
            label="Owner name"
            htmlFor="ownerName"
            required
            error={state.fieldErrors.ownerName}
          >
            <Input
              id="ownerName"
              name="ownerName"
              defaultValue={values.ownerName}
              required
            />
          </Field>
          <p className="text-xs text-[var(--color-ink-3)]">
            Login email:{" "}
            <span className="font-mono text-[var(--color-ink-2)]">
              {values.loginEmail}
            </span>
          </p>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Account</CardTitle>
          <CardDescription>
            Whether you&rsquo;re billing as an individual or a company. You can
            switch later if your structure changes.
          </CardDescription>
        </CardHeader>
        <CardContent className="grid gap-4 px-5 py-5 sm:grid-cols-2">
          <Field
            label="Account name"
            htmlFor="accountName"
            required
            error={state.fieldErrors.accountName}
            className="sm:col-span-2"
          >
            <Input
              id="accountName"
              name="accountName"
              defaultValue={values.accountName}
              required
            />
          </Field>
          <Field
            label="Account type"
            htmlFor="accountType"
            required
            error={state.fieldErrors.accountType}
            className="sm:col-span-2"
          >
            <select
              id="accountType"
              name="accountType"
              defaultValue={values.accountType || "personal"}
              className={SELECT_CLASSES}
            >
              <option value="personal">Personal</option>
              <option value="business">Business</option>
            </select>
          </Field>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Location</CardTitle>
          <CardDescription>
            Used for tax treatment and regulatory pricing. Stored in ISO 3166
            country codes — e.g. <code className="font-mono">US</code>,{" "}
            <code className="font-mono">BD</code>.
          </CardDescription>
        </CardHeader>
        <CardContent className="grid gap-4 px-5 py-5 sm:grid-cols-2">
          <Field
            label="Country"
            htmlFor="countryCode"
            required
            error={state.fieldErrors.countryCode}
          >
            <Input
              id="countryCode"
              name="countryCode"
              defaultValue={values.countryCode}
              required
            />
          </Field>
          <Field
            label="State / Province"
            htmlFor="stateRegion"
            required
            error={state.fieldErrors.stateRegion}
          >
            <Input
              id="stateRegion"
              name="stateRegion"
              defaultValue={values.stateRegion}
              required
            />
          </Field>
        </CardContent>
      </Card>

      {state.formError ? (
        <p role="alert" className="text-sm text-[var(--color-danger)]">
          {state.formError}
        </p>
      ) : null}

      <Button
        type="submit"
        variant="primary"
        size="md"
        disabled={isPending}
        className="self-start"
      >
        {isPending ? "Saving…" : submitLabel}
      </Button>
    </form>
  );
}

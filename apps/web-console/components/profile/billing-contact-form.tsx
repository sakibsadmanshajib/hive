"use client";

import { useActionState } from "react";

import type {
  BillingProfileFieldErrors,
  BillingProfileFormValues,
} from "@/lib/profile-schemas";
import { BusinessTaxForm } from "@/components/profile/business-tax-form";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Field, Input } from "@/components/ui/input";

export interface BillingProfileFormState {
  fieldErrors: BillingProfileFieldErrors;
  formError: string | null;
  values: BillingProfileFormValues;
}

export type BillingProfileFormAction = (
  state: BillingProfileFormState,
  formData: FormData,
) => Promise<BillingProfileFormState>;

interface BillingContactFormProps {
  action: BillingProfileFormAction;
  initialValues: BillingProfileFormValues;
  submitLabel: string;
}

const emptyErrors: BillingProfileFieldErrors = {};

export function BillingContactForm({
  action,
  initialValues,
  submitLabel,
}: BillingContactFormProps) {
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
        name="accountType"
        value={values.accountType}
        readOnly
      />

      <Card>
        <CardHeader>
          <CardTitle>Billing contact</CardTitle>
          <CardDescription>
            Use these fields for later invoicing or checkout notices without
            turning them into a setup gate today.
          </CardDescription>
        </CardHeader>
        <CardContent className="grid gap-4 px-5 py-5">
          <Field
            label="Billing contact name"
            htmlFor="billingContactName"
          >
            <Input
              id="billingContactName"
              name="billingContactName"
              defaultValue={values.billingContactName}
            />
          </Field>

          <Field
            label="Billing contact email"
            htmlFor="billingContactEmail"
            error={state.fieldErrors.billingContactEmail}
          >
            <Input
              id="billingContactEmail"
              name="billingContactEmail"
              type="email"
              defaultValue={values.billingContactEmail}
            />
          </Field>

          <div className="grid gap-4 sm:grid-cols-2">
            <Field label="Country" htmlFor="countryCode">
              <Input
                id="countryCode"
                name="countryCode"
                defaultValue={values.countryCode}
              />
            </Field>
            <Field label="State / Province" htmlFor="stateRegion">
              <Input
                id="stateRegion"
                name="stateRegion"
                defaultValue={values.stateRegion}
              />
            </Field>
          </div>
        </CardContent>
      </Card>

      <BusinessTaxForm
        accountType={values.accountType}
        fieldErrors={state.fieldErrors}
        values={values}
      />

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

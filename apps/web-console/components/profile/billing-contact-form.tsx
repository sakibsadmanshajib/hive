"use client";

import { useActionState } from "react";
import type {
  BillingProfileFieldErrors,
  BillingProfileFormValues,
} from "@/lib/profile-schemas";
import { BusinessTaxForm } from "@/components/profile/business-tax-form";

export interface BillingProfileFormState {
  fieldErrors: BillingProfileFieldErrors;
  formError: string | null;
  values: BillingProfileFormValues;
}

export type BillingProfileFormAction = (
  state: BillingProfileFormState,
  formData: FormData
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
    <form
      action={formAction}
      style={{ display: "grid", gap: "1.5rem", maxWidth: "40rem" }}
    >
      <input type="hidden" name="accountType" value={values.accountType} readOnly />

      <section style={{ display: "grid", gap: "1rem" }}>
        <div style={{ display: "grid", gap: "0.35rem" }}>
          <h2 style={{ margin: 0 }}>Billing contact</h2>
          <p style={{ margin: 0, color: "#6b7280" }}>
            Use these fields for later invoicing or checkout notices without
            turning them into a setup gate today.
          </p>
        </div>

        <div style={{ display: "grid", gap: "0.35rem" }}>
          <label htmlFor="billingContactName">Billing contact name</label>
          <input
            id="billingContactName"
            name="billingContactName"
            defaultValue={values.billingContactName}
            style={{ padding: "0.75rem", border: "1px solid #d1d5db", borderRadius: "0.5rem" }}
          />
        </div>

        <div style={{ display: "grid", gap: "0.35rem" }}>
          <label htmlFor="billingContactEmail">Billing contact email</label>
          <input
            id="billingContactEmail"
            name="billingContactEmail"
            type="email"
            defaultValue={values.billingContactEmail}
            style={{ padding: "0.75rem", border: "1px solid #d1d5db", borderRadius: "0.5rem" }}
          />
          {state.fieldErrors.billingContactEmail && (
            <p role="alert" style={{ color: "#b91c1c", margin: 0 }}>
              {state.fieldErrors.billingContactEmail}
            </p>
          )}
        </div>

        <div
          style={{
            display: "grid",
            gap: "1rem",
            gridTemplateColumns: "repeat(auto-fit, minmax(12rem, 1fr))",
          }}
        >
          <div style={{ display: "grid", gap: "0.35rem" }}>
            <label htmlFor="countryCode">Country</label>
            <input
              id="countryCode"
              name="countryCode"
              defaultValue={values.countryCode}
              style={{ padding: "0.75rem", border: "1px solid #d1d5db", borderRadius: "0.5rem" }}
            />
          </div>

          <div style={{ display: "grid", gap: "0.35rem" }}>
            <label htmlFor="stateRegion">State / Province</label>
            <input
              id="stateRegion"
              name="stateRegion"
              defaultValue={values.stateRegion}
              style={{ padding: "0.75rem", border: "1px solid #d1d5db", borderRadius: "0.5rem" }}
            />
          </div>
        </div>
      </section>

      <BusinessTaxForm
        accountType={values.accountType}
        fieldErrors={state.fieldErrors}
        values={values}
      />

      {state.formError && (
        <p role="alert" style={{ color: "#b91c1c", margin: 0 }}>
          {state.formError}
        </p>
      )}

      <button
        type="submit"
        disabled={isPending}
        style={{
          width: "fit-content",
          padding: "0.75rem 1.25rem",
          backgroundColor: "#111827",
          color: "#fff",
          border: "none",
          borderRadius: "0.5rem",
          cursor: isPending ? "progress" : "pointer",
        }}
      >
        {isPending ? "Saving..." : submitLabel}
      </button>
    </form>
  );
}

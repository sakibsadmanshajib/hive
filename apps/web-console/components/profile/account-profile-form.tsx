"use client";

import { useActionState } from "react";
import type {
  AccountProfileFieldErrors,
  AccountProfileFormValues,
} from "@/lib/profile-schemas";

export interface AccountProfileFormState {
  fieldErrors: AccountProfileFieldErrors;
  formError: string | null;
  values: AccountProfileFormValues;
}

export type AccountProfileFormAction = (
  state: AccountProfileFormState,
  formData: FormData
) => Promise<AccountProfileFormState>;

interface AccountProfileFormProps {
  action: AccountProfileFormAction;
  initialValues: AccountProfileFormValues;
  submitLabel: string;
}

const emptyErrors: AccountProfileFieldErrors = {};

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
    <form action={formAction} style={{ display: "grid", gap: "1rem", maxWidth: "32rem" }}>
      <input type="hidden" name="loginEmail" value={values.loginEmail} readOnly />

      <div style={{ display: "grid", gap: "0.35rem" }}>
        <label htmlFor="ownerName">Owner name</label>
        <input
          id="ownerName"
          name="ownerName"
          defaultValue={values.ownerName}
          required
          style={{ padding: "0.75rem", border: "1px solid #d1d5db", borderRadius: "0.5rem" }}
        />
        {state.fieldErrors.ownerName && (
          <p role="alert" style={{ color: "#b91c1c", margin: 0 }}>
            {state.fieldErrors.ownerName}
          </p>
        )}
      </div>

      <div style={{ display: "grid", gap: "0.35rem" }}>
        <label htmlFor="accountName">Account name</label>
        <input
          id="accountName"
          name="accountName"
          defaultValue={values.accountName}
          required
          style={{ padding: "0.75rem", border: "1px solid #d1d5db", borderRadius: "0.5rem" }}
        />
        {state.fieldErrors.accountName && (
          <p role="alert" style={{ color: "#b91c1c", margin: 0 }}>
            {state.fieldErrors.accountName}
          </p>
        )}
      </div>

      <div style={{ display: "grid", gap: "0.35rem" }}>
        <label htmlFor="accountType">Account type</label>
        <select
          id="accountType"
          name="accountType"
          defaultValue={values.accountType || "personal"}
          style={{ padding: "0.75rem", border: "1px solid #d1d5db", borderRadius: "0.5rem" }}
        >
          <option value="personal">Personal</option>
          <option value="business">Business</option>
        </select>
        {state.fieldErrors.accountType && (
          <p role="alert" style={{ color: "#b91c1c", margin: 0 }}>
            {state.fieldErrors.accountType}
          </p>
        )}
      </div>

      <div style={{ display: "grid", gap: "0.35rem" }}>
        <label htmlFor="countryCode">Country</label>
        <input
          id="countryCode"
          name="countryCode"
          defaultValue={values.countryCode}
          required
          style={{ padding: "0.75rem", border: "1px solid #d1d5db", borderRadius: "0.5rem" }}
        />
        {state.fieldErrors.countryCode && (
          <p role="alert" style={{ color: "#b91c1c", margin: 0 }}>
            {state.fieldErrors.countryCode}
          </p>
        )}
      </div>

      <div style={{ display: "grid", gap: "0.35rem" }}>
        <label htmlFor="stateRegion">State / Province</label>
        <input
          id="stateRegion"
          name="stateRegion"
          defaultValue={values.stateRegion}
          required
          style={{ padding: "0.75rem", border: "1px solid #d1d5db", borderRadius: "0.5rem" }}
        />
        {state.fieldErrors.stateRegion && (
          <p role="alert" style={{ color: "#b91c1c", margin: 0 }}>
            {state.fieldErrors.stateRegion}
          </p>
        )}
      </div>

      <p style={{ margin: 0, color: "#6b7280" }}>Login email: {values.loginEmail}</p>

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

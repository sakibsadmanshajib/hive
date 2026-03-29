import type {
  BillingProfileFieldErrors,
  BillingProfileFormValues,
} from "@/lib/profile-schemas";

interface BusinessTaxFormProps {
  accountType: string;
  fieldErrors: BillingProfileFieldErrors;
  values: BillingProfileFormValues;
}

export function BusinessTaxForm({
  accountType,
  fieldErrors,
  values,
}: BusinessTaxFormProps) {
  const isPersonal = accountType === "personal";

  return (
    <section
      style={{
        display: "grid",
        gap: "1rem",
        padding: "1rem",
        border: "1px solid #d1d5db",
        borderRadius: "0.75rem",
        backgroundColor: "#f9fafb",
      }}
    >
      <div style={{ display: "grid", gap: "0.35rem" }}>
        <h2 style={{ margin: 0 }}>Legal entity and tax</h2>
        <p style={{ margin: 0, color: "#6b7280" }}>
          {isPersonal
            ? "Business-specific fields matter only when later checkout or invoicing requires them."
            : "You can save partial business identity details now and complete the rest only when checkout or invoicing needs them."}
        </p>
      </div>

      <div style={{ display: "grid", gap: "0.35rem" }}>
        <label htmlFor="legalEntityName">Legal entity name</label>
        <input
          id="legalEntityName"
          name="legalEntityName"
          defaultValue={values.legalEntityName}
          style={{ padding: "0.75rem", border: "1px solid #d1d5db", borderRadius: "0.5rem" }}
        />
      </div>

      {isPersonal ? (
        <>
          <input type="hidden" name="legalEntityType" value={values.legalEntityType} readOnly />
          <p style={{ margin: 0, color: "#6b7280" }}>
            Personal accounts default the legal entity type to individual until
            a later billing flow needs something more specific.
          </p>
        </>
      ) : (
        <div style={{ display: "grid", gap: "0.35rem" }}>
          <label htmlFor="legalEntityType">Legal entity type</label>
          <select
            id="legalEntityType"
            name="legalEntityType"
            defaultValue={values.legalEntityType || "private_company"}
            style={{ padding: "0.75rem", border: "1px solid #d1d5db", borderRadius: "0.5rem" }}
          >
            <option value="private_company">Private company</option>
            <option value="public_company">Public company</option>
            <option value="sole_proprietor">Sole proprietor</option>
            <option value="non_profit">Non-profit</option>
            <option value="individual">Individual</option>
          </select>
          {fieldErrors.legalEntityType && (
            <p role="alert" style={{ color: "#b91c1c", margin: 0 }}>
              {fieldErrors.legalEntityType}
            </p>
          )}
        </div>
      )}

      {!isPersonal && (
        <div style={{ display: "grid", gap: "0.35rem" }}>
          <label htmlFor="businessRegistrationNumber">Business registration number</label>
          <input
            id="businessRegistrationNumber"
            name="businessRegistrationNumber"
            defaultValue={values.businessRegistrationNumber}
            style={{ padding: "0.75rem", border: "1px solid #d1d5db", borderRadius: "0.5rem" }}
          />
        </div>
      )}

      <div
        style={{
          display: "grid",
          gap: "1rem",
          gridTemplateColumns: "repeat(auto-fit, minmax(12rem, 1fr))",
        }}
      >
        <div style={{ display: "grid", gap: "0.35rem" }}>
          <label htmlFor="vatNumber">VAT number</label>
          <input
            id="vatNumber"
            name="vatNumber"
            defaultValue={values.vatNumber}
            style={{ padding: "0.75rem", border: "1px solid #d1d5db", borderRadius: "0.5rem" }}
          />
          {fieldErrors.vatNumber && (
            <p role="alert" style={{ color: "#b91c1c", margin: 0 }}>
              {fieldErrors.vatNumber}
            </p>
          )}
        </div>

        <div style={{ display: "grid", gap: "0.35rem" }}>
          <label htmlFor="taxIdType">Tax ID type</label>
          <input
            id="taxIdType"
            name="taxIdType"
            defaultValue={values.taxIdType}
            placeholder="ein, gst, vat"
            style={{ padding: "0.75rem", border: "1px solid #d1d5db", borderRadius: "0.5rem" }}
          />
        </div>
      </div>

      <div style={{ display: "grid", gap: "0.35rem" }}>
        <label htmlFor="taxIdValue">VAT / Tax ID</label>
        <input
          id="taxIdValue"
          name="taxIdValue"
          defaultValue={values.taxIdValue}
          style={{ padding: "0.75rem", border: "1px solid #d1d5db", borderRadius: "0.5rem" }}
        />
        {fieldErrors.taxIdValue && (
          <p role="alert" style={{ color: "#b91c1c", margin: 0 }}>
            {fieldErrors.taxIdValue}
          </p>
        )}
      </div>
    </section>
  );
}

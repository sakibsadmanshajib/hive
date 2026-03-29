import { redirect } from "next/navigation";
import {
  getAccountProfile,
  getBillingProfile,
  getViewer,
  updateBillingProfile,
} from "@/lib/control-plane/client";
import {
  billingProfileSchema,
  type BillingProfileFormValues,
} from "@/lib/profile-schemas";
import {
  BillingContactForm,
  type BillingProfileFormState,
} from "@/components/profile/billing-contact-form";

function toFormValues(
  accountProfile: Awaited<ReturnType<typeof getAccountProfile>>,
  billingProfile: Awaited<ReturnType<typeof getBillingProfile>>
): BillingProfileFormValues {
  return {
    accountType: accountProfile.account_type,
    billingContactName: billingProfile.billing_contact_name,
    billingContactEmail: billingProfile.billing_contact_email,
    legalEntityName: billingProfile.legal_entity_name,
    legalEntityType: billingProfile.legal_entity_type,
    businessRegistrationNumber: billingProfile.business_registration_number,
    vatNumber: billingProfile.vat_number,
    taxIdType: billingProfile.tax_id_type,
    taxIdValue: billingProfile.tax_id_value,
    countryCode: billingProfile.country_code,
    stateRegion: billingProfile.state_region,
  };
}

function readFormValues(formData: FormData): BillingProfileFormValues {
  return {
    accountType: String(formData.get("accountType") ?? ""),
    billingContactName: String(formData.get("billingContactName") ?? ""),
    billingContactEmail: String(formData.get("billingContactEmail") ?? ""),
    legalEntityName: String(formData.get("legalEntityName") ?? ""),
    legalEntityType: String(formData.get("legalEntityType") ?? ""),
    businessRegistrationNumber: String(formData.get("businessRegistrationNumber") ?? ""),
    vatNumber: String(formData.get("vatNumber") ?? ""),
    taxIdType: String(formData.get("taxIdType") ?? ""),
    taxIdValue: String(formData.get("taxIdValue") ?? ""),
    countryCode: String(formData.get("countryCode") ?? ""),
    stateRegion: String(formData.get("stateRegion") ?? ""),
  };
}

export default async function BillingSettingsPage() {
  const viewer = await getViewer();
  if (viewer.user.email_verified === false) {
    redirect("/console/settings/profile");
  }

  const [accountProfile, billingProfile] = await Promise.all([
    getAccountProfile(),
    getBillingProfile(),
  ]);
  const initialValues = toFormValues(accountProfile, billingProfile);

  async function saveBillingProfile(
    _state: BillingProfileFormState,
    formData: FormData
  ): Promise<BillingProfileFormState> {
    "use server";

    const formValues = readFormValues(formData);
    const parsed = billingProfileSchema.safeParse(formValues);

    if (!parsed.success) {
      return {
        fieldErrors: parsed.errors,
        formError: "Please fix the billing fields you provided.",
        values: parsed.values,
      };
    }

    try {
      await updateBillingProfile(parsed.data);
    } catch (error) {
      return {
        fieldErrors: {},
        formError:
          error instanceof Error
            ? error.message
            : "Failed to save your billing profile. Please try again.",
        values: parsed.data,
      };
    }

    redirect("/console/settings/billing");
  }

  return (
    <div style={{ display: "grid", gap: "1.5rem", maxWidth: "48rem" }}>
      <div style={{ display: "grid", gap: "0.5rem" }}>
        <h1 style={{ margin: 0 }}>Billing settings</h1>
        <p style={{ margin: 0, color: "#4b5563" }}>
          Optional until checkout or invoicing.
        </p>
        <p style={{ margin: 0, color: "#6b7280" }}>
          Save whatever billing, legal-entity, and tax context you already
          know, then come back later when a payment or invoice flow needs the
          rest.
        </p>
      </div>

      <BillingContactForm
        action={saveBillingProfile}
        initialValues={initialValues}
        submitLabel="Save billing details"
      />
    </div>
  );
}

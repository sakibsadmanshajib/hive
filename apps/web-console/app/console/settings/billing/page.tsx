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
import { ConsoleShell } from "@/components/app-shell/console-shell";
import { PageHeader } from "@/components/ui/page-header";

function toFormValues(
  accountProfile: Awaited<ReturnType<typeof getAccountProfile>>,
  billingProfile: Awaited<ReturnType<typeof getBillingProfile>>,
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
    businessRegistrationNumber: String(
      formData.get("businessRegistrationNumber") ?? "",
    ),
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
    formData: FormData,
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
    } catch (error: unknown) {
      const message =
        error instanceof Error
          ? error.message
          : "Failed to save your billing profile. Please try again.";
      return {
        fieldErrors: {},
        formError: message,
        values: parsed.data,
      };
    }

    redirect("/console/settings/billing");
  }

  return (
    <ConsoleShell
      workspace={{
        name: viewer.current_account.display_name,
        slug: viewer.current_account.slug,
      }}
      user={{
        email: viewer.user.email,
        name: accountProfile.owner_name || null,
      }}
      active="/console/settings/profile"
      topbar={
        <span className="font-medium text-[var(--color-ink-2)]">
          Billing settings
        </span>
      }
    >
      <PageHeader
        eyebrow="Settings"
        title="Billing settings"
        description="Optional until checkout or invoicing. Save whatever billing, legal-entity, and tax context you already know — come back later when a payment or invoice flow needs the rest."
      />

      <BillingContactForm
        action={saveBillingProfile}
        initialValues={initialValues}
        submitLabel="Save billing details"
      />
    </ConsoleShell>
  );
}

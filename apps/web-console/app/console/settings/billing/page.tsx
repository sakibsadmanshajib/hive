import { redirect } from "next/navigation";

import {
  getBillingProfile,
  updateBillingProfile,
  type AccountProfile,
  type BillingProfile,
} from "@/lib/control-plane/client";
import {
  requireViewer,
  requireAccountProfile,
  tolerate,
} from "@/lib/console/data";
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
import { EmptyState } from "@/components/ui/empty-state";

function toFormValues(
  accountProfile: AccountProfile,
  billingProfile: BillingProfile,
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
  const viewer = await requireViewer();
  if (viewer.user.email_verified === false) {
    redirect("/console/settings/profile");
  }

  const [accountProfile, billingProfile] = await Promise.all([
    requireAccountProfile(),
    tolerate(getBillingProfile()),
  ]);
  // An account with nothing stored yet is a 404 the client turns into a blank
  // billing profile, and the blank form is the right render for it. A read
  // that failed is null, and a blank form is the wrong render for that: it
  // would show saved details as empty and invite the customer to overwrite
  // them (issue #494).
  const initialValues =
    accountProfile && billingProfile
      ? toFormValues(accountProfile, billingProfile)
      : null;

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
        id: viewer.current_account.id,
        name: viewer.current_account.display_name,
        slug: viewer.current_account.slug,
      }}
      memberships={viewer.memberships}
      viewer={viewer}
      user={{
        email: viewer.user.email,
        name: accountProfile?.owner_name || null,
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

      {initialValues ? (
        <BillingContactForm
          action={saveBillingProfile}
          initialValues={initialValues}
          submitLabel="Save billing details"
        />
      ) : (
        <EmptyState
          title="Could not load your billing details"
          description="We could not reach the billing profile service, so this form is not showing what is currently saved. Refresh to try again."
        />
      )}
    </ConsoleShell>
  );
}

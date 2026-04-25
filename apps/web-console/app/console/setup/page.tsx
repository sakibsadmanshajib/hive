import { redirect } from "next/navigation";

import {
  getAccountProfile,
  getViewer,
  updateAccountProfile,
} from "@/lib/control-plane/client";
import {
  accountProfileSchema,
  type AccountProfileFormValues,
} from "@/lib/profile-schemas";
import {
  AccountProfileForm,
  type AccountProfileFormState,
} from "@/components/profile/account-profile-form";
import { ConsoleShell } from "@/components/app-shell/console-shell";
import { PageHeader } from "@/components/ui/page-header";

function toFormValues(
  profile: Awaited<ReturnType<typeof getAccountProfile>>,
): AccountProfileFormValues {
  return {
    ownerName: profile.owner_name,
    loginEmail: profile.login_email,
    accountName: profile.display_name,
    accountType: profile.account_type,
    countryCode: profile.country_code,
    stateRegion: profile.state_region,
  };
}

function readFormValues(formData: FormData): AccountProfileFormValues {
  return {
    ownerName: String(formData.get("ownerName") ?? ""),
    loginEmail: String(formData.get("loginEmail") ?? ""),
    accountName: String(formData.get("accountName") ?? ""),
    accountType: String(formData.get("accountType") ?? ""),
    countryCode: String(formData.get("countryCode") ?? ""),
    stateRegion: String(formData.get("stateRegion") ?? ""),
  };
}

export default async function SetupPage() {
  const [profile, viewer] = await Promise.all([
    getAccountProfile(),
    getViewer(),
  ]);
  const initialValues = toFormValues(profile);

  async function saveProfile(
    _state: AccountProfileFormState,
    formData: FormData,
  ): Promise<AccountProfileFormState> {
    "use server";

    const formValues = readFormValues(formData);
    const parsed = accountProfileSchema.safeParse(formValues);

    if (!parsed.success) {
      return {
        fieldErrors: parsed.errors,
        formError: "Please complete the required fields.",
        values: parsed.values,
      };
    }

    try {
      await updateAccountProfile(parsed.data);
    } catch (error: unknown) {
      const message =
        error instanceof Error
          ? error.message
          : "Failed to save your profile. Please try again.";
      return {
        fieldErrors: {},
        formError: message,
        values: parsed.data,
      };
    }

    redirect("/console");
  }

  return (
    <ConsoleShell
      workspace={{
        name: viewer.current_account.display_name,
        slug: viewer.current_account.slug,
      }}
      user={{ email: viewer.user.email, name: profile.owner_name || null }}
      active="/console"
      topbar={
        <span className="font-medium text-[var(--color-ink-2)]">
          Workspace setup
        </span>
      }
    >
      <PageHeader
        eyebrow="Onboarding"
        title="Complete your workspace profile"
        description="Three short sections — owner, account and location. Save what you have now; you can return to refine billing and tax details later."
      />

      <AccountProfileForm
        action={saveProfile}
        initialValues={initialValues}
        submitLabel="Save and continue"
      />
    </ConsoleShell>
  );
}

import { redirect } from "next/navigation";

import {
  updateAccountProfile,
  type AccountProfile,
} from "@/lib/control-plane/client";
import {
  requireViewer,
  requireAccountProfile,
} from "@/lib/console/data";
import {
  accountProfileSchema,
  type AccountProfileFormValues,
} from "@/lib/profile-schemas";
import {
  AccountProfileForm,
  type AccountProfileFormState,
} from "@/components/profile/account-profile-form";
import { ConsoleShell } from "@/components/app-shell/console-shell";
import { EmailSettingsCard } from "@/components/email-settings-card";
import { PageHeader } from "@/components/ui/page-header";
import { EmptyState } from "@/components/ui/empty-state";

function toFormValues(
  profile: AccountProfile,
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

interface ProfileSettingsPageProps {
  searchParams: Promise<{ saved?: string }>;
}

export default async function ProfileSettingsPage({
  searchParams,
}: ProfileSettingsPageProps) {
  const params = await searchParams;
  const justSaved = params.saved === "1";
  const [viewer, profile] = await Promise.all([
    requireViewer(),
    requireAccountProfile(),
  ]);
  // null is "we could not read your profile", not "your profile is blank".
  // Seeding the form from it would show saved values as empty and invite the
  // customer to overwrite them with those blanks (issue #494).
  const initialValues = profile ? toFormValues(profile) : null;

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

    redirect("/console/settings/profile?saved=1");
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
      user={{ email: viewer.user.email, name: profile?.owner_name || null }}
      active="/console/settings/profile"
      topbar={
        <span className="font-medium text-[var(--color-ink-2)]">
          Profile settings
        </span>
      }
    >
      <PageHeader
        eyebrow="Settings"
        title="Profile settings"
        description="Maintain the minimal account profile here. This page stays available even when your email is not yet verified — resend verification or change the login email below."
      />

      <div className="flex flex-col gap-6">
        <EmailSettingsCard
          email={viewer.user.email}
          emailVerified={viewer.user.email_verified}
        />

        {initialValues ? (
          <AccountProfileForm
            action={saveProfile}
            initialValues={initialValues}
            submitLabel="Save profile"
            justSaved={justSaved}
          />
        ) : (
          <EmptyState
            title="Could not load your profile"
            description="We could not reach the profile service, so this form is not showing what is currently saved. Refresh to try again."
          />
        )}
      </div>
    </ConsoleShell>
  );
}

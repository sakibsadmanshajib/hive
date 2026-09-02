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

export default async function SetupPage() {
  const [profile, viewer] = await Promise.all([
    requireAccountProfile(),
    requireViewer(),
  ]);
  // A fresh account has no profile row and requireAccountProfile() hands back
  // the empty needs-setup one, which is exactly what this form is for. null is
  // the other case: the profile could not be read at all. Seeding a blank form
  // from that would invite the customer to save those blanks over data that is
  // still there, so the page says so instead (issue #494).
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

    redirect("/console");
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

      {initialValues ? (
        <AccountProfileForm
          action={saveProfile}
          initialValues={initialValues}
          submitLabel="Save and continue"
          justSaved={false}
        />
      ) : (
        <EmptyState
          title="Could not load your profile"
          description="We could not reach the profile service, so this form is not showing what is currently saved. Refresh to try again."
        />
      )}
    </ConsoleShell>
  );
}

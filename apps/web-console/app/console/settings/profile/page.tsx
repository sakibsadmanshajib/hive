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
import { EmailSettingsCard } from "@/components/email-settings-card";

function toFormValues(profile: Awaited<ReturnType<typeof getAccountProfile>>): AccountProfileFormValues {
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

export default async function ProfileSettingsPage() {
  const viewer = await getViewer();
  const profile = await getAccountProfile();
  const initialValues = toFormValues(profile);

  async function saveProfile(
    _state: AccountProfileFormState,
    formData: FormData
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
    } catch (error) {
      return {
        fieldErrors: {},
        formError:
          error instanceof Error
            ? error.message
            : "Failed to save your profile. Please try again.",
        values: parsed.data,
      };
    }

    redirect("/console/settings/profile");
  }

  return (
    <div style={{ display: "grid", gap: "1.5rem", maxWidth: "48rem" }}>
      <div style={{ display: "grid", gap: "0.5rem" }}>
        <h1 style={{ margin: 0 }}>Profile settings</h1>
        <p style={{ margin: 0, color: "#4b5563" }}>
          Maintain the minimal account profile here. This page stays available
          even when your email is not yet verified.
        </p>
        <p style={{ margin: 0, color: "#6b7280" }}>
          Resend verification email or change the login address here before you
          unlock the rest of the console.
        </p>
      </div>

      <EmailSettingsCard
        email={viewer.user.email}
        emailVerified={viewer.user.email_verified}
      />

      <section
        style={{
          padding: "1rem",
          border: "1px solid #d1d5db",
          borderRadius: "0.75rem",
          display: "grid",
          gap: "1rem",
        }}
      >
        <div style={{ display: "grid", gap: "0.35rem" }}>
          <h2 style={{ margin: 0 }}>Account profile</h2>
          <p style={{ margin: 0, color: "#6b7280" }}>
            Update the owner, workspace, and location fields used before any
            billing flow begins.
          </p>
        </div>

        <AccountProfileForm
          action={saveProfile}
          initialValues={initialValues}
          submitLabel="Save profile"
        />
      </section>
    </div>
  );
}

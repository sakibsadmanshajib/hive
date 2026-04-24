import { redirect } from "next/navigation";
import {
  getAccountProfile,
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

export default async function SetupPage() {
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

    redirect("/console");
  }

  return (
    <div style={{ display: "grid", gap: "1rem" }}>
      <div style={{ display: "grid", gap: "0.5rem" }}>
        <h1 style={{ margin: 0 }}>Complete your workspace profile</h1>
        <p style={{ margin: 0, color: "#4b5563", maxWidth: "40rem" }}>
          Finish the minimal setup now so the dashboard can reflect your account
          details without pulling billing or tax fields into onboarding.
        </p>
      </div>

      <AccountProfileForm
        action={saveProfile}
        initialValues={initialValues}
        submitLabel="Save and continue"
      />
    </div>
  );
}

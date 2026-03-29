export interface AccountProfileFormValues {
  ownerName: string;
  loginEmail: string;
  accountName: string;
  accountType: string;
  countryCode: string;
  stateRegion: string;
}

export type AccountProfileFieldErrors = Partial<
  Record<keyof AccountProfileFormValues, string>
>;

type AccountProfileParseSuccess = {
  success: true;
  data: AccountProfileFormValues;
};

type AccountProfileParseFailure = {
  success: false;
  errors: AccountProfileFieldErrors;
  values: AccountProfileFormValues;
};

type AccountProfileParseResult =
  | AccountProfileParseSuccess
  | AccountProfileParseFailure;

function normalizeValue(value: unknown) {
  return typeof value === "string" ? value.trim() : "";
}

export const accountProfileSchema = {
  safeParse(input: Partial<AccountProfileFormValues>): AccountProfileParseResult {
    const values: AccountProfileFormValues = {
      ownerName: normalizeValue(input.ownerName),
      loginEmail: normalizeValue(input.loginEmail),
      accountName: normalizeValue(input.accountName),
      accountType: normalizeValue(input.accountType).toLowerCase(),
      countryCode: normalizeValue(input.countryCode).toUpperCase(),
      stateRegion: normalizeValue(input.stateRegion),
    };

    const errors: AccountProfileFieldErrors = {};

    if (!values.ownerName) {
      errors.ownerName = "Owner name is required.";
    }
    if (!values.loginEmail) {
      errors.loginEmail = "Login email is required.";
    }
    if (!values.accountName) {
      errors.accountName = "Account name is required.";
    }
    if (!values.accountType) {
      errors.accountType = "Account type is required.";
    } else if (values.accountType !== "personal" && values.accountType !== "business") {
      errors.accountType = "Account type must be personal or business.";
    }
    if (!values.countryCode) {
      errors.countryCode = "Country is required.";
    }
    if (!values.stateRegion) {
      errors.stateRegion = "State / Province is required.";
    }

    if (Object.keys(errors).length > 0) {
      return { success: false, errors, values };
    }

    return { success: true, data: values };
  },
};

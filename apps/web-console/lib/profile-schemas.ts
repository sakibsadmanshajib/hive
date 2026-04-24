export interface AccountProfileFormValues {
  ownerName: string;
  loginEmail: string;
  accountName: string;
  accountType: string;
  countryCode: string;
  stateRegion: string;
}

export interface BillingProfileFormValues {
  accountType: string;
  billingContactName: string;
  billingContactEmail: string;
  legalEntityName: string;
  legalEntityType: string;
  businessRegistrationNumber: string;
  vatNumber: string;
  taxIdType: string;
  taxIdValue: string;
  countryCode: string;
  stateRegion: string;
}

export type AccountProfileFieldErrors = Partial<
  Record<keyof AccountProfileFormValues, string>
>;

export type BillingProfileFieldErrors = Partial<
  Record<keyof BillingProfileFormValues, string>
>;

type SchemaSuccess<T> = {
  success: true;
  data: T;
};

type SchemaFailure<T, E> = {
  success: false;
  errors: E;
  values: T;
};

type AccountProfileParseResult =
  | SchemaSuccess<AccountProfileFormValues>
  | SchemaFailure<AccountProfileFormValues, AccountProfileFieldErrors>;

type BillingProfileParseResult =
  | SchemaSuccess<BillingProfileFormValues>
  | SchemaFailure<BillingProfileFormValues, BillingProfileFieldErrors>;

const billingIdentifierPattern = /^[A-Za-z0-9][A-Za-z0-9 /-]{1,63}$/;

function normalizeValue(value: string | null | undefined): string {
  return value?.trim() ?? "";
}

function defaultBillingLegalEntityType(accountType: string): string {
  return accountType === "personal" ? "individual" : "private_company";
}

function isValidEmailShape(value: string): boolean {
  return /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(value);
}

function isValidLegalEntityType(value: string): boolean {
  switch (value) {
    case "individual":
    case "sole_proprietor":
    case "private_company":
    case "public_company":
    case "non_profit":
      return true;
    default:
      return false;
  }
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

export const billingProfileSchema = {
  safeParse(input: Partial<BillingProfileFormValues>): BillingProfileParseResult {
    const values: BillingProfileFormValues = {
      accountType: normalizeValue(input.accountType).toLowerCase(),
      billingContactName: normalizeValue(input.billingContactName),
      billingContactEmail: normalizeValue(input.billingContactEmail),
      legalEntityName: normalizeValue(input.legalEntityName),
      legalEntityType: normalizeValue(input.legalEntityType).toLowerCase(),
      businessRegistrationNumber: normalizeValue(input.businessRegistrationNumber),
      vatNumber: normalizeValue(input.vatNumber).toUpperCase(),
      taxIdType: normalizeValue(input.taxIdType).toLowerCase(),
      taxIdValue: normalizeValue(input.taxIdValue).toUpperCase(),
      countryCode: normalizeValue(input.countryCode).toUpperCase(),
      stateRegion: normalizeValue(input.stateRegion),
    };

    const errors: BillingProfileFieldErrors = {};

    if (!values.accountType) {
      errors.accountType = "Account type is required.";
    } else if (values.accountType !== "personal" && values.accountType !== "business") {
      errors.accountType = "Account type must be personal or business.";
    }

    if (!errors.accountType && !values.legalEntityType) {
      values.legalEntityType = defaultBillingLegalEntityType(values.accountType);
    }

    if (values.legalEntityType && !isValidLegalEntityType(values.legalEntityType)) {
      errors.legalEntityType =
        "Legal entity type must be individual, sole proprietor, private company, public company, or non-profit.";
    }

    if (
      values.billingContactEmail &&
      !isValidEmailShape(values.billingContactEmail)
    ) {
      errors.billingContactEmail = "Billing contact email must be a valid email address.";
    }

    if (values.vatNumber && !billingIdentifierPattern.test(values.vatNumber)) {
      errors.vatNumber =
        "VAT number must contain only letters, numbers, spaces, slashes, or hyphens.";
    }

    if (values.taxIdValue && !billingIdentifierPattern.test(values.taxIdValue)) {
      errors.taxIdValue =
        "VAT / Tax ID must contain only letters, numbers, spaces, slashes, or hyphens.";
    }

    if (Object.keys(errors).length > 0) {
      return { success: false, errors, values };
    }

    return { success: true, data: values };
  },
};

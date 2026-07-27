import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";

const mockRedirect = vi.fn(() => {
  throw new Error("NEXT_REDIRECT");
});
const mockGetSession = vi.fn();
const mockGetUser = vi.fn();
const mockCreateClient = vi.fn(() => ({
  auth: {
    getUser: mockGetUser,
    getSession: mockGetSession,
  },
}));

vi.mock("next/navigation", () => ({
  redirect: mockRedirect,
}));

vi.mock("next/headers", () => ({
  cookies: vi.fn(async () => ({
    get: vi.fn(() => undefined),
    getAll: vi.fn(() => []),
  })),
}));

vi.mock("../lib/supabase/server", () => ({
  createClient: mockCreateClient,
}));

// The shell pulls in next-intl's useTranslations, which needs a provider this
// test has no reason to stand up. Both stubs keep the assertions on what the
// page computes rather than on how the chrome renders.
vi.mock("@/components/app-shell/console-shell", () => ({
  ConsoleShell: ({ children }: { children: React.ReactNode }) => (
    <div data-testid="console-shell">{children}</div>
  ),
}));

vi.mock("@/components/profile/billing-contact-form", () => ({
  BillingContactForm: ({ initialValues }: { initialValues: unknown }) => (
    <div data-testid="billing-form">{JSON.stringify(initialValues)}</div>
  ),
}));

const VIEWER_PAYLOAD = {
  user: { id: "u1", email: "qa@example.test", email_verified: true },
  current_account: {
    id: "a1",
    display_name: "QA Workspace",
    account_type: "personal",
    role: "owner",
  },
  memberships: [
    { account_id: "a1", display_name: "QA Workspace", role: "owner", status: "active" },
  ],
  permissions: ["workspace.settings"],
};

function jsonResponse(status: number, body: unknown): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    text: async () => JSON.stringify(body),
  } as unknown as Response;
}

/** Routes fetch by path so each endpoint can answer with its own status. */
function routeFetch(statuses: { profile: number; billingProfile: number }) {
  return vi.fn(async (url: string) => {
    if (url.endsWith("/api/v1/viewer")) {
      return jsonResponse(200, VIEWER_PAYLOAD);
    }
    if (url.endsWith("/api/v1/accounts/current/billing-profile")) {
      if (statuses.billingProfile !== 200) {
        return jsonResponse(statuses.billingProfile, { error: "profile not found" });
      }
      return jsonResponse(200, {
        billing_contact_name: "Ada Owner",
        billing_contact_email: "ada@example.test",
        legal_entity_name: "Ada Ltd",
        legal_entity_type: "private_company",
        business_registration_number: "BRN-1",
        vat_number: "VAT-1",
        tax_id_type: "gst",
        tax_id_value: "TAX-1",
        country_code: "CA",
        state_region: "ON",
      });
    }
    if (url.endsWith("/api/v1/accounts/current/profile")) {
      if (statuses.profile !== 200) {
        return jsonResponse(statuses.profile, { error: "profile not found" });
      }
      return jsonResponse(200, {
        owner_name: "Ada Owner",
        login_email: "ada@example.test",
        display_name: "QA Workspace",
        account_type: "personal",
        country_code: "CA",
        state_region: "ON",
        profile_setup_complete: true,
      });
    }
    throw new Error(`unexpected fetch: ${url}`);
  });
}

describe("getBillingProfile 404 tolerance", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    process.env.CONTROL_PLANE_BASE_URL = "http://localhost:8081";
    mockGetUser.mockResolvedValue({
      data: { user: { id: "u1", email: "qa@example.test" } },
      error: null,
    });
    mockGetSession.mockResolvedValue({
      data: { session: { access_token: "test-token" } },
    });
  });

  // Sibling getAccountProfile has special-cased 404 since the profile-setup
  // work; getBillingProfile was missed, so a 404 threw inside the billing
  // page's Promise.all and took down the whole Server Components tree.
  it("returns an empty billing profile instead of throwing on 404", async () => {
    vi.stubGlobal("fetch", routeFetch({ profile: 404, billingProfile: 404 }));

    const { getBillingProfile } = await import("../lib/control-plane/client");
    const profile = await getBillingProfile();

    expect(profile).toEqual({
      billing_contact_name: "",
      billing_contact_email: "",
      legal_entity_name: "",
      legal_entity_type: "",
      business_registration_number: "",
      vat_number: "",
      tax_id_type: "",
      tax_id_value: "",
      country_code: "",
      state_region: "",
    });
  });

  it("still decodes a stored billing profile", async () => {
    vi.stubGlobal("fetch", routeFetch({ profile: 200, billingProfile: 200 }));

    const { getBillingProfile } = await import("../lib/control-plane/client");
    const profile = await getBillingProfile();

    expect(profile.billing_contact_name).toBe("Ada Owner");
    expect(profile.legal_entity_type).toBe("private_company");
    expect(profile.country_code).toBe("CA");
  });

  it("still throws on a non-404 failure so real outages are not masked", async () => {
    vi.stubGlobal("fetch", routeFetch({ profile: 200, billingProfile: 503 }));

    const { getBillingProfile } = await import("../lib/control-plane/client");
    await expect(getBillingProfile()).rejects.toThrow();
  });
});

describe("app/console/settings/billing/page.tsx", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    process.env.CONTROL_PLANE_BASE_URL = "http://localhost:8081";
    mockGetUser.mockResolvedValue({
      data: { user: { id: "u1", email: "qa@example.test" } },
      error: null,
    });
    mockGetSession.mockResolvedValue({
      data: { session: { access_token: "test-token" } },
    });
  });

  it("renders for an account that has no profile rows at all", async () => {
    vi.stubGlobal("fetch", routeFetch({ profile: 404, billingProfile: 404 }));

    const mod = await import("../app/console/settings/billing/page");
    const page = await mod.default();
    render(page);

    expect(mockRedirect).not.toHaveBeenCalled();
    const form = screen.getByTestId("billing-form");
    expect(JSON.parse(form.textContent ?? "{}")).toEqual({
      accountType: "",
      billingContactName: "",
      billingContactEmail: "",
      legalEntityName: "",
      legalEntityType: "",
      businessRegistrationNumber: "",
      vatNumber: "",
      taxIdType: "",
      taxIdValue: "",
      countryCode: "",
      stateRegion: "",
    });
  });
});

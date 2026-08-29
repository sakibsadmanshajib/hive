/**
 * Issue #762: twenty two of the twenty five registered feature gates have no
 * runtime reader. The value writes to public.tenant_settings correctly and
 * nothing ever looks at it, so flipping the switch changes nothing at all.
 *
 * Until the tenant-resolution fix in this same change, the page could not load
 * at all, so nobody was ever misled. Making it load turns a dormant problem
 * into a live one: an operator would be handed twenty two switches and told
 * changes take effect across the API and apps. On a product sold on
 * auditability, a control that lies about what it controls is worse than no
 * control, so the row has to say so.
 */
import { describe, expect, it, vi } from "vitest";
import { render, screen, within } from "@testing-library/react";

vi.mock("next/navigation", () => ({
  useRouter: () => ({ refresh: vi.fn(), push: vi.fn() }),
}));

import { FeatureGateManager } from "@/components/feature-gates/feature-gate-manager";
import type { FeatureGate } from "@/lib/control-plane/client";

function gate(overrides: Partial<FeatureGate>): FeatureGate {
  return {
    key: "ENABLE_RAG",
    label: "Agent RAG capability",
    category: "agents",
    enabled: false,
    manageable: true,
    enforced: true,
    ...overrides,
  };
}

describe("FeatureGateManager enforcement honesty", () => {
  it("marks a gate nothing reads as stored but not enforced", () => {
    render(
      <FeatureGateManager
        gates={[
          gate({
            key: "ENABLE_SSO_SAML",
            label: "SAML 2.0 SSO",
            category: "sso",
            enforced: false,
          }),
        ]}
      />,
    );

    const row = screen.getByRole("listitem");
    expect(within(row).getByText(/not enforced yet/i)).toBeTruthy();
  });

  it("leaves an enforced gate unmarked, so the notice keeps its meaning", () => {
    render(<FeatureGateManager gates={[gate({ enforced: true })]} />);

    expect(screen.queryByText(/not enforced yet/i)).toBeNull();
  });

  it("marks every unenforced row rather than the group heading only", () => {
    render(
      <FeatureGateManager
        gates={[
          gate({ key: "ENABLE_AUDIT_SINK_ELK", label: "Audit sink: ELK", category: "audit_sink", enforced: false }),
          gate({ key: "ENABLE_AUDIT_SINK_LOKI", label: "Audit sink: Loki", category: "audit_sink", enforced: false }),
          gate({ key: "ENABLE_RAG", label: "Agent RAG capability", category: "agents", enforced: true }),
        ]}
      />,
    );

    expect(screen.getAllByText(/not enforced yet/i)).toHaveLength(2);
  });

  it("treats a gate with no enforced field as not enforced", () => {
    // Rolling-deploy default. A control-plane that predates this change omits
    // the field, and the two possible errors are not symmetric: an unenforced
    // gate rendered as enforced is the exact harm #762 describes, while an
    // enforced gate rendered as unenforced is a redundant notice for one
    // deploy window. So the absence resolves to unenforced, which is also why
    // the flag is read as "anything but true" at the render site rather than
    // defaulted once in the decoder where nothing could test it.
    const withoutFlag: FeatureGate = gate({});
    delete (withoutFlag as { enforced?: boolean }).enforced;

    render(<FeatureGateManager gates={[withoutFlag]} />);

    expect(screen.getByText(/not enforced yet/i)).toBeTruthy();
  });

  it("gives the notice a text form, not colour alone", () => {
    // WCAG 1.4.1. A marker that only exists as a muted swatch is invisible to
    // a screen reader and to anyone who cannot separate the two greys, which
    // is precisely the audience a compliance-facing page cannot afford to
    // mislead.
    render(<FeatureGateManager gates={[gate({ enforced: false })]} />);

    const notice = screen.getByText(/not enforced yet/i);
    expect(notice.textContent?.trim().length ?? 0).toBeGreaterThan(0);
  });
});

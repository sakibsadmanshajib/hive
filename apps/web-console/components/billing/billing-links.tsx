import Link from "next/link";
import { Bell, FileText, Gauge } from "lucide-react";

import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";

// Issue #543: /console/billing/alerts, /console/billing/budget and
// /console/billing/invoices are real, working, shell-wrapped pages that nothing
// in the app linked to. A grep of the repository found references only in the
// e2e specs that navigate to them directly, and `main a` on the deployed
// billing page returned only its own tabs. The pages were reachable by typing a
// URL and no other way.
//
// They are a card here rather than three more tabs on the Billing page because
// the tabs address the same endpoint with a query parameter while these are
// separate routes with their own data, and because the pages already set
// active="/console/billing" so the rail keeps Billing lit when the reader
// arrives.
const LINKS: ReadonlyArray<{
  href: string;
  label: string;
  description: string;
  icon: React.ReactNode;
}> = [
  {
    href: "/console/billing/alerts",
    label: "Spend alerts",
    description:
      "Email and webhook notifications when month-to-date spend crosses a percentage of the cap.",
    icon: <Bell size={14} />,
  },
  {
    href: "/console/billing/budget",
    label: "Budget",
    description: "Soft and hard caps on monthly workspace spend.",
    icon: <Gauge size={14} />,
  },
  {
    href: "/console/billing/invoices",
    label: "Workspace invoices",
    description: "Monthly invoices for this workspace, with PDF downloads.",
    icon: <FileText size={14} />,
  },
];

export function BillingLinks() {
  return (
    <Card>
      <CardHeader>
        <CardTitle>Spend controls</CardTitle>
        <CardDescription>
          Caps, alerts and invoices for this workspace.
        </CardDescription>
      </CardHeader>
      <CardContent className="px-5 py-5">
        <ul className="flex flex-col gap-1">
          {LINKS.map((link) => (
            <li key={link.href}>
              <Link
                href={link.href}
                className="flex items-start gap-2.5 rounded-md px-2 py-2 text-sm transition-colors duration-[var(--duration-fast)] hover:bg-[var(--color-surface-inset)]"
              >
                <span className="mt-0.5 shrink-0 text-[var(--color-ink-3)]">
                  {link.icon}
                </span>
                <span className="flex min-w-0 flex-col gap-0.5">
                  <span className="font-medium text-[var(--color-ink)]">
                    {link.label}
                  </span>
                  <span className="text-2xs text-[var(--color-ink-3)]">
                    {link.description}
                  </span>
                </span>
              </Link>
            </li>
          ))}
        </ul>
      </CardContent>
    </Card>
  );
}

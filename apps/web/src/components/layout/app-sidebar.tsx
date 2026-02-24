import Link from "next/link";

import { cn } from "../../lib/utils";

const navItems = [
  { href: "/auth", label: "Auth" },
  { href: "/chat", label: "Chat" },
  { href: "/billing", label: "Billing" },
];

export function AppSidebar({ className }: { className?: string }) {
  return (
    <nav aria-label="Primary" className={cn("flex flex-col gap-2", className)}>
      <Link className="rounded-md px-3 py-2 text-sm font-medium hover:bg-muted" href="/">
        Home
      </Link>
      {navItems.map((item) => (
        <Link key={item.href} className="rounded-md px-3 py-2 text-sm font-medium hover:bg-muted" href={item.href}>
          {item.label}
        </Link>
      ))}
    </nav>
  );
}

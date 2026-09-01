import Link from "next/link";
import { ArrowRight } from "lucide-react";

import type { Viewer } from "@/lib/control-plane/client";
import { ConsoleShell } from "@/components/app-shell/console-shell";
import { PageHeader } from "@/components/ui/page-header";
import { buttonVariants } from "@/components/ui/button";
import { cn } from "@/lib/cn";

interface ConsoleNotFoundProps {
  viewer: Viewer;
  /** Owner name for the topbar identity; null when the profile fetch failed. */
  ownerName: string | null;
  /** Rail entry to keep lit, e.g. "/console/catalog". */
  active: string;
  /** Section label for the topbar, e.g. "Model catalog". */
  section: string;
  /** Section eyebrow above the title, e.g. "Build". */
  eyebrow: string;
  title: string;
  description: string;
  backHref: string;
  backLabel: string;
}

/**
 * The not-found state for a resource that a signed-in viewer asked for and the
 * console cannot produce: an unknown model id, an API key that is not theirs.
 *
 * Why this exists rather than notFound() (issue #1652). Measured on Next 16.3.2
 * and again on 16.3.4: a notFound() raised part-way through a render answers
 * 404 with an <html id="__next_error__"> document whose body is a single empty
 * hidden div. The real 404 travels only in the Flight payload and is painted by
 * the client, so the first paint is blank, and with JavaScript off it stays
 * blank. An unmatched URL is unaffected, because the router resolves that
 * before rendering starts and app/not-found.tsx renders into the initial HTML.
 * A segment-scoped app/console/not-found.tsx and an app/global-not-found.tsx
 * were both built and measured against this app: neither changes the shape,
 * because boundary depth is not what selects that path. This component is
 * server-rendered on the ordinary render path, so it has no blank first paint.
 *
 * Where it may be used, and where it may not. This is for a data miss reached
 * by a viewer who is already entitled to the shell that surrounds it, and it
 * discloses nothing that viewer could not already see: an id that exists but is
 * invisible to this workspace renders exactly what a nonexistent id renders.
 *
 * It must NOT be used for the role-gated surfaces. /console/providers,
 * /console/feature-gates and /console/marketplace answer notFound() to a viewer
 * without the role precisely so the response cannot confirm that the surface
 * exists (the #947/#948/#949 family), and the shell they must not render is the
 * one this component renders: workspace name, signed-in identity, and the full
 * rail including the operator-only group. Those keep notFound() and the
 * shell-free boundary in app/not-found.tsx, unchanged.
 */
export function ConsoleNotFound({
  viewer,
  ownerName,
  active,
  section,
  eyebrow,
  title,
  description,
  backHref,
  backLabel,
}: ConsoleNotFoundProps) {
  return (
    <ConsoleShell
      workspace={{
        id: viewer.current_account.id,
        name: viewer.current_account.display_name,
        slug: viewer.current_account.slug,
      }}
      memberships={viewer.memberships}
      viewer={viewer}
      user={{ email: viewer.user.email, name: ownerName }}
      active={active}
      topbar={
        <span className="font-medium text-[var(--color-ink-2)]">{section}</span>
      }
    >
      <PageHeader
        eyebrow={eyebrow}
        title={title}
        description={description}
        actions={
          <Link
            href={backHref}
            className={cn(buttonVariants({ variant: "secondary", size: "sm" }))}
          >
            {backLabel}
            <ArrowRight size={14} aria-hidden="true" />
          </Link>
        }
      />
    </ConsoleShell>
  );
}

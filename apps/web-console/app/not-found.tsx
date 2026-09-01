import Link from "next/link";

import { AuthShell } from "@/components/app-shell/auth-shell";
import { buttonVariants } from "@/components/ui/button";

/**
 * The console's only 404 boundary. Without it Next.js served its own stock,
 * unstyled "This page could not be found" for every miss, including the
 * deliberate ones.
 *
 * Deliberate is the important word. `notFound()` is the access control on
 * /console/providers, /console/feature-gates and /console/marketplace (the
 * #947/#948/#949 family): those routes answer 404 to a viewer without the
 * role precisely so that the response cannot be used to confirm the surface
 * exists. That makes ConsoleShell the one thing this page must not render.
 * The shell carries the workspace name, the signed-in identity, and the full
 * thirteen-entry rail including the operator-only group, so a "branded" 404
 * built on it would hand back, in the 404 body, everything the 404 status was
 * withholding.
 *
 * AuthShell instead: same brand, same type, same colours, and no navigation
 * at all -- it is the chrome the signed-out pages already use. The single way
 * back is /console, the console's front door, which every viewer can already
 * reach and which discloses nothing about what lies past it (an unauthorised
 * visitor is bounced to sign-in by app/console/layout.tsx exactly as before).
 * No "did you mean" list, no route suggestions, no echo of the path that
 * missed.
 *
 * What this page cannot do, measured on Next 16.3.2 and again on 16.3.4
 * (issue #1652): reach the initial HTML for a notFound() raised part-way
 * through a render. The App Router answers those with an
 * <html id="__next_error__"> document whose body is a single empty hidden div,
 * leaving the 404 to be painted by the client from the Flight payload, so the
 * first paint is blank and stays blank with JavaScript off. An unmatched URL is
 * resolved before rendering starts and does get this page in the initial HTML,
 * which is why the two paths looked so different. A segment-scoped
 * app/console/not-found.tsx and an app/global-not-found.tsx were both built and
 * measured against this app; neither changes it.
 *
 * So the two data-miss pages that used to call notFound() (the catalog detail
 * page, the API key limits page) render components/app-shell/console-not-found
 * in place instead, server-side, with no blank paint. The three role-gated
 * pages still call notFound() and still land here: the 404 status is the access
 * control on those, and a 200 would confirm the surface exists.
 */
export default function NotFound() {
  return (
    <AuthShell
      eyebrow="404"
      title="Page not found"
      subtitle="This page does not exist, or it is not available on this account."
    >
      <Link
        href="/console"
        className={buttonVariants({ variant: "primary", size: "md" })}
      >
        Back to the console
      </Link>
    </AuthShell>
  );
}

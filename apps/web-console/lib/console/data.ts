import { cache } from "react";
import { redirect, unstable_rethrow } from "next/navigation";

import {
  getAccountProfile,
  getViewer,
  type AccountProfile,
  type Viewer,
} from "@/lib/control-plane/client";

/**
 * The console's data-fetch seam (issue #494).
 *
 * Every console Server Component reads the control-plane, and a Server
 * Component that lets a read throw takes its whole subtree with it: from a
 * page that is the page, from app/console/layout.tsx that is every console
 * route at once. Before this module the tolerance was per call site and
 * inconsistent — nineteen bare getViewer() calls, six bare
 * getAccountProfile() calls, and twelve sites that each caught the same
 * failure into a slightly different ad-hoc fallback, sometimes two lines
 * apart in one Promise.all.
 *
 * The two reads every page performs now resolve here once per request, so a
 * page added later inherits the behaviour by importing from this module
 * rather than by remembering to write a catch. __tests__/
 * console-fetch-tolerance-guard.test.ts fails the build if one does not.
 *
 * What "tolerance" means is decided per read, and the rule is that the UI
 * never claims a state the system does not have:
 *
 *   - A viewer cannot degrade. Without it there is no workspace, no
 *     membership list and no shell, and the honest response to "we cannot
 *     establish who you are" is the sign-in page, which is where an expired
 *     session already lands.
 *   - A profile can be absent for a real reason (a fresh account has no row,
 *     and the control-plane 404s), which is not the same as a profile the
 *     console failed to read. The first is an AccountProfile; the second is
 *     null. Collapsing them would render a completed account as one that
 *     still needs setup.
 *   - Everything else goes through tolerate() and renders an explicit
 *     unknown. Never a zero, never an empty list.
 *
 * Every catch here calls unstable_rethrow() first. Next.js signals redirect(),
 * notFound() and "this route cannot be prerendered, it read cookies" by
 * throwing, so a catch-all that does not re-raise those swallows the
 * framework's own control flow: a build-time DynamicServerError would be read
 * as a failed viewer fetch and answered with a redirect to sign-in. That is a
 * trap every ad-hoc `.catch` at a call site was already exposed to, and
 * closing it once here is the point of having one seam.
 */

/**
 * requireViewer resolves the signed-in viewer, or sends the caller to sign-in.
 *
 * cache() scopes one fetch to one request, so the layout and the page beneath
 * it share a single answer instead of racing two independent chances to fail
 * (the viewer endpoint was being called twice per navigation).
 *
 * redirect() signals by throwing, so this deliberately does not catch around
 * it: the throw is how Next.js performs the redirect.
 */
export const requireViewer = cache(async (): Promise<Viewer> => {
  try {
    return await getViewer();
  } catch (error) {
    unstable_rethrow(error);
    console.error("console: could not load viewer", error);
    redirect("/auth/sign-in");
  }
});

/**
 * requireAccountProfile resolves the account profile, or null when it cannot
 * be read.
 *
 * null means unknown, and only unknown. A brand-new account with no profile
 * row still resolves to a profile object (getAccountProfile turns the
 * control-plane's 404 into EMPTY_ACCOUNT_PROFILE), because "you have not
 * filled this in yet" is a state the system really is in and the setup form
 * has to render for it.
 *
 * Callers that only need a display name can write `profile?.owner_name`.
 * Callers that seed an editable form must handle null explicitly: rendering a
 * blank form over a profile that merely failed to load invites the customer
 * to save those blanks over their real data.
 */
export const requireAccountProfile = cache(
  async (): Promise<AccountProfile | null> => {
    try {
      return await getAccountProfile();
    } catch (error) {
      unstable_rethrow(error);
      console.error("console: could not load account profile", error);
      return null;
    }
  },
);

/**
 * tolerate resolves a failed console read to null so the page can render the
 * rest of itself and say plainly that one region is unavailable.
 *
 * null is the codebase's existing signal for this (app/console/page.tsx
 * distinguishes a null analytics result from an empty one so an outage cannot
 * render as "no requests"); this is that idiom under a name, so it is
 * greppable and so the guard test can recognise it.
 *
 * Use it for reads whose absence the surface can state. Do not use it to
 * substitute a plausible value: a balance that failed to load is unknown, not
 * zero, and zero is below every threshold a customer can set.
 */
/**
 * tolerate for a read whose own answer can legitimately be null.
 *
 * `tolerate()` reports failure as null, so for a read like getBudgetThreshold
 * or getBudget, whose null already means "nothing is configured", the two
 * meanings collide and the surface renders "none set" for an outage. That
 * invites a customer to overwrite a threshold that is still in force, and it
 * is the same false-state defect as a failed balance rendering as zero, just
 * hidden one level down in the types rather than visible as a `?? 0`.
 *
 * The box keeps them apart: null is unreadable, `{ value: null }` is a real
 * absence. Reach for this whenever null already means something on the read
 * you are wrapping.
 *
 * Five reads in lib/control-plane/client.ts answer null in their own right,
 * found by asking the types rather than by grepping the names:
 * getBudgetThreshold, getBudget, getUsageWindows, getWorkspaceInvoice and
 * getInvoicePdfUrl. They do not all want the same treatment, so each is
 * accounted for here.
 *
 * getUsageWindows is exempt. It never throws: every non-2xx answer already
 * becomes its own null, which the surface renders as "unavailable". There are
 * no two meanings to keep apart, so bare tolerate() is correct at its call
 * site in app/console/api-keys/page.tsx and the box would be noise.
 *
 * getWorkspaceInvoice and getInvoicePdfUrl are unwrapped today, and safe only
 * because nothing tolerates them yet. Wrapping either in bare tolerate() is
 * what this box exists to stop; reach for it there.
 *
 * getBudgetThreshold and getBudget need more than the box, so neither uses it.
 * Both are read through an owner-only permission, so a member gets a 403, and
 * a refusal is a real answer about the viewer rather than an outage. Two
 * states cannot carry three. Their call sites spell out ok, forbidden and
 * unreadable instead: app/console/billing/page.tsx,
 * app/console/billing/budget/page.tsx, and app/console/members/page.tsx,
 * which is where the shape comes from.
 *
 * That leaves the box with no call site in the tree right now. It stays
 * because it is the right answer for a nullable read that cannot be refused,
 * which is exactly the two invoice reads above.
 */
export async function tolerateBoxed<T>(
  read: Promise<T>,
): Promise<{ value: T } | null> {
  const value = await tolerate(read.then((v) => ({ v })));
  return value === null ? null : { value: value.v };
}

export async function tolerate<T>(read: Promise<T>): Promise<T | null> {
  try {
    return await read;
  } catch (error) {
    unstable_rethrow(error);
    console.error("console: control-plane read failed", error);
    return null;
  }
}

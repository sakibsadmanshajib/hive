"use client";

import { createClient } from "@/lib/supabase/browser";
import { useSearchParams } from "next/navigation";
import { useEffect, useState, type FormEvent } from "react";
import { ArrowRight } from "lucide-react";

import { AuthShell } from "@/components/app-shell/auth-shell";
import { Button } from "@/components/ui/button";
import { Field, Input } from "@/components/ui/input";
import { toUserFacingAuthMessage } from "@/lib/auth/auth-error";
import { appendNextParam, resolveNextTarget } from "@/lib/auth/next-target";
import { navigate } from "@/lib/navigate";

export default function SignInPage() {
  const supabase = createClient();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  // Read during render rather than from an effect, because this value decides
  // the page heading. Every route in this app is dynamically rendered (the root
  // layout awaits cookies() through next-intl's getRequestConfig), so
  // useSearchParams resolves on the server render as well as on the client:
  // server markup and the first client render agree, which is what keeps this
  // out of hydration-mismatch territory. An effect-populated value could not do
  // that -- it renders once with no `next` and then swaps, and the user who
  // would see that swap is exactly the chat user this branch is for.
  //
  // It also carries the ?next= target the user arrived with -- e.g. an OAuth
  // consent round-trip started on chat -- into the "Create one" link on first
  // paint, so signing up instead of signing in no longer strands them on the
  // plain console dashboard (issue found in live UI/UX pass, 2026-07-26).
  const nextParam = useSearchParams().get("next");
  // This form has no `action` and its inputs carry no `name`, so a submit that
  // lands before React attaches onSubmit is handled natively: the browser does
  // a GET to the current path with an empty form data set, which REPLACES the
  // query string and silently discards `?next=`. Sign-in then succeeds on the
  // reloaded page and sends the user to /console, abandoning whatever journey
  // sent them here (OWUI nightly run 31042840516: the OIDC consent round-trip
  // died exactly this way -- /auth/sign-in?next=%2Foauth%2Fconsent... became
  // /auth/sign-in? one second later, still visible in the deployed console's
  // server HTML today). Gating the submit button on mount makes the
  // pre-hydration submit impossible instead of merely unlikely; a disabled
  // default button also blocks implicit Enter submission.
  const [hydrated, setHydrated] = useState(false);

  useEffect(() => {
    setHydrated(true);
  }, []);

  async function handleSubmit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setError(null);
    setLoading(true);

    const { error } = await supabase.auth.signInWithPassword({
      email,
      password,
    });

    if (error) {
      setError(toUserFacingAuthMessage(error.message));
      setLoading(false);
      return;
    }

    // Hard navigation forces the browser to re-issue the request with the
    // freshly-written sb-*-auth-token cookies. router.push + router.refresh
    // ran the next request before the document-cookie write had been
    // observed by the SSR pipeline, which made middleware see no user and
    // bounce /console back to /auth/sign-in.
    navigate(resolveNextTarget(nextParam));
  }

  // A user who lands here mid OAuth consent came from a Hive product surface
  // (chat today), not from the developer console, so the console pitch is both
  // wrong and disorienting: they clicked "Continue with Hive" on a chat login
  // screen and were answered with a page about API keys, credits and usage
  // analytics. Resolving through the shared allow-list rather than sniffing the
  // raw param means an unlisted or hostile `next` can never reach this branch --
  // resolveNextTarget only ever returns "/oauth/consent" or
  // "/oauth/consent?..." for genuine consent round-trips.
  const fromConsent = resolveNextTarget(nextParam).startsWith("/oauth/consent");

  return (
    <AuthShell
      eyebrow="Welcome back"
      title={fromConsent ? "Sign in to Hive" : "Sign in to your console"}
      subtitle={
        fromConsent
          ? "Use your Hive account to continue."
          : "Manage API keys, credits, and usage analytics for your workspace."
      }
      footer={
        <>
          Don&rsquo;t have an account?{" "}
          <a
            href={appendNextParam("/auth/sign-up", nextParam)}
            className="text-[var(--color-accent)] underline-offset-4 hover:underline"
          >
            Create one
          </a>
        </>
      }
    >
      <form onSubmit={handleSubmit} className="flex flex-col gap-4">
        <Field label="Email" htmlFor="email" required>
          <Input
            id="email"
            type="email"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            placeholder="you@example.com"
            required
            autoComplete="email"
          />
        </Field>
        <Field
          label="Password"
          htmlFor="password"
          required
          hint={
            <a
              href="/auth/forgot-password"
              className="text-[var(--color-ink-3)] hover:text-[var(--color-ink)] underline-offset-4 hover:underline"
            >
              Forgot password?
            </a>
          }
        >
          <Input
            id="password"
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            required
            autoComplete="current-password"
          />
        </Field>
        {error ? (
          <p
            role="alert"
            className="text-xs text-[var(--color-danger)] leading-tight"
          >
            {error}
          </p>
        ) : null}
        <Button
          type="submit"
          variant="primary"
          size="lg"
          disabled={loading || !hydrated}
          className="w-full"
        >
          {loading ? (
            "Signing in…"
          ) : (
            <>
              Continue
              <ArrowRight size={14} />
            </>
          )}
        </Button>
      </form>
    </AuthShell>
  );
}

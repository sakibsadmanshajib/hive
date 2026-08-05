"use client";

import { createClient } from "@/lib/supabase/browser";
import { useState, useRef, useEffect, type FormEvent } from "react";
import { Mail } from "lucide-react";

import { AuthShell } from "@/components/app-shell/auth-shell";
import { Button } from "@/components/ui/button";
import { Field, Input } from "@/components/ui/input";
import { toUserFacingAuthMessage } from "@/lib/auth/auth-error";
import { appendNextParam } from "@/lib/auth/next-target";

// Minimal ambient type for the Cloudflare Turnstile widget. The full SDK type
// is not installed as a dev dependency; we only need the render/remove surface.
declare global {
  interface Window {
    turnstile?: {
      render: (
        container: string | HTMLElement,
        options: {
          sitekey: string;
          callback: (token: string) => void;
          "expired-callback": () => void;
          "error-callback": () => void;
          theme?: "light" | "dark" | "auto";
        }
      ) => string;
      remove: (widgetId: string) => void;
    };
  }
}

const TURNSTILE_SITE_KEY =
  typeof process !== "undefined"
    ? process.env.NEXT_PUBLIC_TURNSTILE_SITE_KEY ?? ""
    : "";

export default function SignUpPage() {
  const supabase = createClient();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [success, setSuccess] = useState(false);
  const [loading, setLoading] = useState(false);
  const [captchaToken, setCaptchaToken] = useState<string | null>(null);
  // Populated post-mount (window is unavailable during SSR). Carries an
  // inbound ?next= target (e.g. an OAuth consent round-trip started on chat)
  // through to the "Sign in" cross-link and into the verification-email
  // redirect, so it survives the signup -> confirm-email -> callback chain
  // instead of dropping the user onto the plain console dashboard (issue
  // found in live UI/UX pass, 2026-07-26).
  const [nextParam, setNextParam] = useState<string | null>(null);
  // Same pre-hydration submit hazard as /auth/sign-in (see the comment there):
  // no form `action` and no input `name` attributes mean a submit fired before
  // onSubmit is attached does a native GET that wipes the query string, taking
  // `?next=` with it, so the signup half of the OIDC round-trip lands on
  // /console instead of consent. Gate the submit button on mount.
  const [hydrated, setHydrated] = useState(false);

  const turnstileContainerRef = useRef<HTMLDivElement | null>(null);
  const widgetIdRef = useRef<string | null>(null);

  useEffect(() => {
    setNextParam(new URLSearchParams(window.location.search).get("next"));
    setHydrated(true);
  }, []);

  // Load the Turnstile script and render the widget when a site key is
  // configured. When NEXT_PUBLIC_TURNSTILE_SITE_KEY is unset (local dev),
  // the widget is skipped and captchaToken stays null, which the precheck
  // endpoint accepts (server-side key also absent in dev).
  useEffect(() => {
    if (!TURNSTILE_SITE_KEY || !turnstileContainerRef.current) return;

    const scriptId = "cf-turnstile-script";
    const alreadyLoaded = document.getElementById(scriptId) !== null;

    function renderWidget() {
      if (!window.turnstile || !turnstileContainerRef.current) return;
      widgetIdRef.current = window.turnstile.render(
        turnstileContainerRef.current,
        {
          sitekey: TURNSTILE_SITE_KEY,
          callback: (token) => setCaptchaToken(token),
          "expired-callback": () => setCaptchaToken(null),
          "error-callback": () => setCaptchaToken(null),
          theme: "light",
        }
      );
    }

    if (alreadyLoaded) {
      renderWidget();
      return;
    }

    const script = document.createElement("script");
    script.id = scriptId;
    script.src = "https://challenges.cloudflare.com/turnstile/v0/api.js";
    script.async = true;
    script.defer = true;
    script.onload = renderWidget;
    document.head.appendChild(script);

    return () => {
      if (widgetIdRef.current && window.turnstile) {
        window.turnstile.remove(widgetIdRef.current);
      }
    };
  }, []);

  async function handleSubmit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setError(null);
    setLoading(true);

    try {
      // Abuse-prevention precheck (issue #116): rate limit, disposable-domain
      // blocklist, and Turnstile CAPTCHA. The call goes through a Next.js Route
      // Handler so CONTROL_PLANE_BASE_URL stays server-side only.
      const precheckRes = await fetch("/api/auth/signup-precheck", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ email, captcha_token: captchaToken ?? "" }),
      });

      if (!precheckRes.ok) {
        let msg =
          "We could not complete your sign-up. Please try again or use a different email address.";
        try {
          const body: unknown = await precheckRes.json();
          if (
            body !== null &&
            typeof body === "object" &&
            "error" in body &&
            typeof (body as Record<string, unknown>).error === "string" &&
            ((body as Record<string, unknown>).error as string).length > 0
          ) {
            msg = (body as Record<string, unknown>).error as string;
          }
        } catch {
          // Ignore parse errors; generic message is already set.
        }
        setError(msg);
        return;
      }

      const appUrl =
        process.env.NEXT_PUBLIC_APP_URL ??
        (typeof window !== "undefined"
          ? window.location.origin
          : "http://localhost:3000");

      const { error: signUpError } = await supabase.auth.signUp({
        email,
        password,
        options: {
          emailRedirectTo: appendNextParam(
            `${appUrl}/auth/callback`,
            nextParam,
          ),
        },
      });

      if (signUpError) {
        setError(toUserFacingAuthMessage(signUpError.message));
        return;
      }

      setSuccess(true);
    } catch {
      setError(
        "An unexpected error occurred. Please check your connection and try again."
      );
    } finally {
      setLoading(false);
    }
  }

  if (success) {
    return (
      <AuthShell
        eyebrow="Almost there"
        title="Check your email"
        subtitle={
          <>
            We sent a verification link to{" "}
            <span className="font-mono text-[var(--color-ink)]">{email}</span>.
            Open it on this device to activate your account.
          </>
        }
      >
        <div className="flex items-center gap-3 rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-inset)] px-4 py-3 text-sm text-[var(--color-ink-2)]">
          <Mail size={16} className="text-[var(--color-accent)] shrink-0" />
          <span>Email may take a minute to arrive. Check your spam folder.</span>
        </div>
      </AuthShell>
    );
  }

  return (
    <AuthShell
      eyebrow="Get started"
      title="Create your Hive account"
      subtitle="Pay only for what you use, in BDT."
      footer={
        <>
          Already have an account?{" "}
          <a
            href={appendNextParam("/auth/sign-in", nextParam)}
            className="text-[var(--color-accent)] underline-offset-4 hover:underline"
          >
            Sign in
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
          hint="At least 8 characters."
        >
          <Input
            id="password"
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            required
            autoComplete="new-password"
            minLength={8}
          />
        </Field>
        {TURNSTILE_SITE_KEY ? (
          <div ref={turnstileContainerRef} className="flex justify-center" />
        ) : null}
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
          {loading ? "Creating account…" : "Create account"}
        </Button>
      </form>
    </AuthShell>
  );
}

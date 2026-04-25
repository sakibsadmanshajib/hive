"use client";

import { createClient } from "@/lib/supabase/browser";
import { useState, type FormEvent } from "react";
import { ArrowRight } from "lucide-react";

import { AuthShell } from "@/components/app-shell/auth-shell";
import { Button } from "@/components/ui/button";
import { Field, Input } from "@/components/ui/input";

export default function SignInPage() {
  const supabase = createClient();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);

  async function handleSubmit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setError(null);
    setLoading(true);

    const { error } = await supabase.auth.signInWithPassword({
      email,
      password,
    });

    if (error) {
      setError(error.message);
      setLoading(false);
      return;
    }

    // Hard navigation forces the browser to re-issue the request with the
    // freshly-written sb-*-auth-token cookies. router.push + router.refresh
    // ran the next request before the document-cookie write had been
    // observed by the SSR pipeline, which made middleware see no user and
    // bounce /console back to /auth/sign-in.
    window.location.assign("/console");
  }

  return (
    <AuthShell
      eyebrow="Welcome back"
      title="Sign in to your console"
      subtitle="Manage API keys, credits, and usage analytics for your workspace."
      footer={
        <>
          Don&rsquo;t have an account?{" "}
          <a
            href="/auth/sign-up"
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
          disabled={loading}
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

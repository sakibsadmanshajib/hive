"use client";

import { createClient } from "@/lib/supabase/browser";
import { useState, type FormEvent } from "react";
import { Mail } from "lucide-react";

import { AuthShell } from "@/components/app-shell/auth-shell";
import { Button } from "@/components/ui/button";
import { Field, Input } from "@/components/ui/input";

export default function ForgotPasswordPage() {
  const supabase = createClient();
  const [email, setEmail] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [success, setSuccess] = useState(false);
  const [loading, setLoading] = useState(false);

  async function handleSubmit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setError(null);
    setLoading(true);

    const appUrl =
      process.env.NEXT_PUBLIC_APP_URL ??
      (typeof window !== "undefined"
        ? window.location.origin
        : "http://localhost:3000");

    const { error } = await supabase.auth.resetPasswordForEmail(email, {
      redirectTo: `${appUrl}/auth/callback?next=/auth/reset-password`,
    });

    if (error) {
      setError(error.message);
      setLoading(false);
      return;
    }

    setSuccess(true);
    setLoading(false);
  }

  if (success) {
    return (
      <AuthShell
        eyebrow="Check your inbox"
        title="Reset link sent"
        subtitle={
          <>
            If an account exists for{" "}
            <span className="font-mono text-[var(--color-ink)]">{email}</span>,
            you&rsquo;ll receive an email with a link to set a new password.
          </>
        }
      >
        <div className="flex items-center gap-3 rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-inset)] px-4 py-3 text-sm text-[var(--color-ink-2)]">
          <Mail size={16} className="text-[var(--color-accent)] shrink-0" />
          <span>The link expires in 60 minutes.</span>
        </div>
        <a
          href="/auth/sign-in"
          className="text-xs text-[var(--color-ink-3)] hover:text-[var(--color-ink)] underline-offset-4 hover:underline"
        >
          Back to sign in
        </a>
      </AuthShell>
    );
  }

  return (
    <AuthShell
      eyebrow="Account recovery"
      title="Reset your password"
      subtitle="Enter the email associated with your Hive account. We&rsquo;ll send you a secure link to choose a new password."
      footer={
        <a
          href="/auth/sign-in"
          className="text-[var(--color-ink-3)] hover:text-[var(--color-ink)] underline-offset-4 hover:underline"
        >
          Back to sign in
        </a>
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
          {loading ? "Sending…" : "Send reset link"}
        </Button>
      </form>
    </AuthShell>
  );
}

"use client";

import { createClient } from "@/lib/supabase/browser";
import { useState, type FormEvent } from "react";
import { Mail } from "lucide-react";

import { AuthShell } from "@/components/app-shell/auth-shell";
import { Button } from "@/components/ui/button";
import { Field, Input } from "@/components/ui/input";

export default function SignUpPage() {
  const supabase = createClient();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
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

    const { error } = await supabase.auth.signUp({
      email,
      password,
      options: {
        emailRedirectTo: `${appUrl}/auth/callback`,
      },
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
      subtitle="Free to start. Pay only for what you use, in BDT."
      footer={
        <>
          Already have an account?{" "}
          <a
            href="/auth/sign-in"
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
          {loading ? "Creating account…" : "Create account"}
        </Button>
      </form>
    </AuthShell>
  );
}

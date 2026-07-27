"use client";

import { useState, type FormEvent } from "react";

import { BASE_PATH } from "@/lib/base-path";
import { createClient } from "@/lib/supabase/browser";
import { AuthShell } from "@/components/brand";
import { Field, INPUT_CLASS, buttonClass } from "@/components/ui";

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

    const { error } = await supabase.auth.signInWithPassword({ email, password });

    if (error) {
      setError(error.message);
      setLoading(false);
      return;
    }

    // Hard navigation, not router.push: forces the browser to re-issue the
    // request with the freshly-written sb-*-auth-token cookies so middleware
    // sees the session on the very next request (same fix as web-console).
    // BASE_PATH is explicit because window.location bypasses Next's basePath.
    window.location.assign(`${BASE_PATH}/tasks`);
  }

  return (
    <AuthShell
      eyebrow="Agent workspace"
      title="Sign in to run agent tasks"
      subtitle="Use your Hive account. This workspace is separate from chat, so signing in here does not sign you out of it."
    >
      <form onSubmit={handleSubmit} className="flex flex-col gap-4" noValidate>
        <Field label="Email" htmlFor="email" required>
          <input
            id="email"
            type="email"
            required
            autoComplete="email"
            placeholder="you@example.com"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            className={INPUT_CLASS}
          />
        </Field>
        <Field label="Password" htmlFor="password" required>
          <input
            id="password"
            type="password"
            required
            autoComplete="current-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            className={INPUT_CLASS}
          />
        </Field>
        {error ? (
          <p
            role="alert"
            className="text-xs leading-tight text-[var(--color-danger)]"
          >
            {error}
          </p>
        ) : null}
        <button
          type="submit"
          disabled={loading}
          className={`${buttonClass("accent", "lg")} w-full`}
        >
          {loading ? "Signing in…" : "Sign in"}
        </button>
      </form>
    </AuthShell>
  );
}

"use client";

import { createClient } from "@/lib/supabase/browser";
import { useState, type FormEvent } from "react";

import { AuthShell } from "@/components/app-shell/auth-shell";
import { Button } from "@/components/ui/button";
import { Field, Input } from "@/components/ui/input";

export default function ResetPasswordPage() {
  const supabase = createClient();
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);

  async function handleSubmit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setError(null);

    if (password !== confirm) {
      setError("Passwords do not match.");
      return;
    }

    setLoading(true);

    const { error } = await supabase.auth.updateUser({ password });

    if (error) {
      setError(error.message);
      setLoading(false);
      return;
    }

    // Hard navigation so middleware sees the refreshed session cookies.
    window.location.assign("/console");
  }

  return (
    <AuthShell
      eyebrow="Final step"
      title="Set a new password"
      subtitle="Pick something memorable but uncrackable. At least 8 characters."
    >
      <form onSubmit={handleSubmit} className="flex flex-col gap-4">
        <Field label="New password" htmlFor="password" required>
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
        <Field label="Confirm new password" htmlFor="confirm" required>
          <Input
            id="confirm"
            type="password"
            value={confirm}
            onChange={(e) => setConfirm(e.target.value)}
            required
            autoComplete="new-password"
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
          {loading ? "Updating…" : "Update password"}
        </Button>
      </form>
    </AuthShell>
  );
}

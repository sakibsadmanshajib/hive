"use client";

import { useState } from "react";
import { useRouter } from "next/navigation";

import { createClient } from "@/lib/supabase/browser";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Field, Input } from "@/components/ui/input";

interface EmailSettingsCardProps {
  email: string;
  emailVerified: boolean;
}

export function EmailSettingsCard({
  email,
  emailVerified,
}: EmailSettingsCardProps) {
  const router = useRouter();
  const supabase = createClient();
  const [nextEmail, setNextEmail] = useState(email);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [resending, setResending] = useState(false);
  const [updating, setUpdating] = useState(false);

  async function handleResendVerification() {
    setError(null);
    setNotice(null);
    setResending(true);

    const appUrl =
      process.env.NEXT_PUBLIC_APP_URL ??
      (typeof window !== "undefined"
        ? window.location.origin
        : "http://localhost:3000");

    const { error: resendError } = await supabase.auth.signInWithOtp({
      email,
      options: {
        shouldCreateUser: false,
        emailRedirectTo: `${appUrl}/auth/callback?next=/console/settings/profile&hive_verify=1`,
      },
    });

    if (resendError) {
      setError(resendError.message);
      setResending(false);
      return;
    }

    setNotice(
      "Verification email sent. Use the link in that email to unlock the rest of the console.",
    );
    setResending(false);
  }

  async function handleChangeEmail() {
    setError(null);
    setNotice(null);
    setUpdating(true);

    const trimmedEmail = nextEmail.trim();
    const { error: updateError } = await supabase.auth.updateUser({
      email: trimmedEmail,
    });

    if (updateError) {
      setError(updateError.message);
      setUpdating(false);
      return;
    }

    setNotice("Email change requested. Check your inbox to confirm it.");
    setUpdating(false);
    router.refresh();
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Email settings</CardTitle>
        <CardDescription>
          Login email is{" "}
          <strong className="font-medium text-[var(--color-ink)]">
            {email}
          </strong>
          . {emailVerified
            ? "Your login email is verified."
            : "Your login email still needs verification."}
        </CardDescription>
      </CardHeader>
      <CardContent className="grid gap-4 px-5 py-5">
        {!emailVerified ? (
          <div>
            <Button
              type="button"
              variant="primary"
              size="md"
              onClick={() => void handleResendVerification()}
              disabled={resending}
            >
              {resending ? "Sending…" : "Resend verification email"}
            </Button>
          </div>
        ) : null}

        <div className="grid gap-3 sm:grid-cols-[1fr_auto] sm:items-end">
          <Field label="Change email" htmlFor="nextEmail">
            <Input
              id="nextEmail"
              type="email"
              value={nextEmail}
              onChange={(event) => setNextEmail(event.target.value)}
              autoComplete="email"
            />
          </Field>
          <Button
            type="button"
            variant="secondary"
            size="md"
            onClick={() => void handleChangeEmail()}
            disabled={updating}
          >
            {updating ? "Updating…" : "Change email"}
          </Button>
        </div>

        {notice ? (
          <p role="status" className="text-xs text-[var(--color-success)]">
            {notice}
          </p>
        ) : null}
        {error ? (
          <p role="alert" className="text-xs text-[var(--color-danger)]">
            {error}
          </p>
        ) : null}
      </CardContent>
    </Card>
  );
}

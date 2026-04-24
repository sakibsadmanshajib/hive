"use client";

import { useState } from "react";
import { useRouter } from "next/navigation";
import { createClient } from "@/lib/supabase/browser";

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
      (typeof window !== "undefined" ? window.location.origin : "http://localhost:3000");

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

    setNotice("Verification email sent. Use the link in that email to unlock the rest of the console.");
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
    <section
      style={{
        padding: "1rem",
        border: "1px solid #d1d5db",
        borderRadius: "0.75rem",
        display: "grid",
        gap: "0.75rem",
      }}
    >
      <div style={{ display: "grid", gap: "0.25rem" }}>
        <h2 style={{ margin: 0 }}>Email settings</h2>
        <p style={{ margin: 0, color: "#4b5563" }}>
          Current login email: <strong>{email}</strong>
        </p>
        <p style={{ margin: 0, color: "#6b7280" }}>
          {emailVerified
            ? "Your login email is verified."
            : "Your login email still needs verification."}
        </p>
      </div>

      {!emailVerified && (
        <div style={{ display: "flex", flexWrap: "wrap", gap: "0.75rem" }}>
          <button
            type="button"
            onClick={handleResendVerification}
            disabled={resending}
            style={{
              padding: "0.75rem 1rem",
              backgroundColor: "#111827",
              color: "#fff",
              border: "none",
              borderRadius: "0.5rem",
              cursor: resending ? "progress" : "pointer",
            }}
          >
            {resending ? "Sending..." : "Resend verification email"}
          </button>
        </div>
      )}

      <div style={{ display: "grid", gap: "0.35rem", maxWidth: "24rem" }}>
        <label htmlFor="nextEmail">Change email</label>
        <input
          id="nextEmail"
          type="email"
          value={nextEmail}
          onChange={(event) => setNextEmail(event.target.value)}
          autoComplete="email"
          style={{ padding: "0.75rem", border: "1px solid #d1d5db", borderRadius: "0.5rem" }}
        />
        <button
          type="button"
          onClick={handleChangeEmail}
          disabled={updating}
          style={{
            width: "fit-content",
            padding: "0.75rem 1rem",
            backgroundColor: "#fff",
            color: "#111827",
            border: "1px solid #111827",
            borderRadius: "0.5rem",
            cursor: updating ? "progress" : "pointer",
          }}
        >
          {updating ? "Updating..." : "Change email"}
        </button>
      </div>

      {notice && (
        <p role="status" style={{ margin: 0, color: "#166534" }}>
          {notice}
        </p>
      )}
      {error && (
        <p role="alert" style={{ margin: 0, color: "#b91c1c" }}>
          {error}
        </p>
      )}
    </section>
  );
}

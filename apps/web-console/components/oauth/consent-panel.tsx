"use client";

import { useEffect, useState } from "react";

import { createClient } from "@/lib/supabase/browser";
import { navigate } from "@/lib/navigate";
import { AuthShell } from "@/components/app-shell/auth-shell";
import { Button } from "@/components/ui/button";

interface ConsentPanelProps {
  authorizationId: string | null;
}

interface ConsentDetails {
  clientName: string;
  scopes: string[];
}

type ConsentStatus = "loading" | "ready" | "error";

/**
 * Builds the /auth/sign-in?next=... URL that round-trips an unauthenticated
 * visitor back to this exact consent request once they've logged in.
 * Exported for direct unit testing without rendering the component.
 */
export function buildSignInRedirect(authorizationId: string): string {
  const returnPath = `/oauth/consent?authorization_id=${encodeURIComponent(authorizationId)}`;
  return `/auth/sign-in?next=${encodeURIComponent(returnPath)}`;
}

export function ConsentPanel({ authorizationId }: ConsentPanelProps) {
  const [status, setStatus] = useState<ConsentStatus>("loading");
  const [details, setDetails] = useState<ConsentDetails | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [deciding, setDeciding] = useState(false);

  useEffect(() => {
    let cancelled = false;
    const supabase = createClient();

    async function load() {
      if (!authorizationId) {
        setError(
          "Missing authorization request. Ask the app you're signing in from to try again.",
        );
        setStatus("error");
        return;
      }

      const sessionResult = await supabase.auth.getSession();
      if (cancelled) return;

      if (!sessionResult.data.session) {
        navigate(buildSignInRedirect(authorizationId));
        return;
      }

      const authDetailsResult =
        await supabase.auth.oauth.getAuthorizationDetails(authorizationId);
      if (cancelled) return;

      if (authDetailsResult.error) {
        setError(authDetailsResult.error.message);
        setStatus("error");
        return;
      }

      const authDetails = authDetailsResult.data;

      if ("redirect_url" in authDetails) {
        // User already consented to these scopes — hand off immediately.
        navigate(authDetails.redirect_url);
        return;
      }

      setDetails({
        clientName: authDetails.client.name,
        scopes: authDetails.scope.split(" ").filter(Boolean),
      });
      setStatus("ready");
    }

    load().catch((err: unknown) => {
      if (cancelled) return;
      setError(
        err instanceof Error
          ? err.message
          : "Failed to load the authorization request.",
      );
      setStatus("error");
    });

    return () => {
      cancelled = true;
    };
  }, [authorizationId]);

  async function handleDecision(action: "approve" | "deny") {
    if (!authorizationId) return;
    setDeciding(true);
    setError(null);

    const supabase = createClient();

    try {
      const decisionResult =
        action === "approve"
          ? await supabase.auth.oauth.approveAuthorization(authorizationId, {
              skipBrowserRedirect: true,
            })
          : await supabase.auth.oauth.denyAuthorization(authorizationId, {
              skipBrowserRedirect: true,
            });

      if (decisionResult.error) {
        setError(decisionResult.error.message);
        return;
      }

      navigate(decisionResult.data.redirect_url);
    } catch (err: unknown) {
      setError(
        err instanceof Error
          ? err.message
          : "Failed to submit your decision.",
      );
    } finally {
      setDeciding(false);
    }
  }

  if (status === "loading") {
    return (
      <AuthShell title="Checking your authorization request…">
        <p className="text-sm text-[var(--color-ink-3)]">One moment.</p>
      </AuthShell>
    );
  }

  if (status === "error" || !details) {
    return (
      <AuthShell eyebrow="Sign-in request" title="Can't continue">
        <p role="alert" className="text-sm text-[var(--color-danger)]">
          {error ?? "Something went wrong loading this request."}
        </p>
      </AuthShell>
    );
  }

  return (
    <AuthShell
      eyebrow="Sign-in request"
      title={`Let ${details.clientName} connect to Hive?`}
      subtitle="Review what this app is asking to access before continuing."
    >
      <div className="flex flex-col gap-4">
        <ul className="flex flex-col gap-1.5 text-sm text-[var(--color-ink-2)]">
          {details.scopes.map((scope) => (
            <li key={scope} className="flex items-center gap-2">
              <span
                aria-hidden="true"
                className="h-1 w-1 rounded-full bg-[var(--color-ink-3)]"
              />
              {scope}
            </li>
          ))}
        </ul>
        {error ? (
          <p role="alert" className="text-xs text-[var(--color-danger)] leading-tight">
            {error}
          </p>
        ) : null}
        <div className="flex gap-3">
          <Button
            type="button"
            variant="primary"
            size="lg"
            className="flex-1"
            disabled={deciding}
            onClick={() => handleDecision("approve")}
          >
            Approve
          </Button>
          <Button
            type="button"
            variant="secondary"
            size="lg"
            className="flex-1"
            disabled={deciding}
            onClick={() => handleDecision("deny")}
          >
            Deny
          </Button>
        </div>
      </div>
    </AuthShell>
  );
}

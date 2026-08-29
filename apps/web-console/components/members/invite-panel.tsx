"use client";

import { useRouter } from "next/navigation";
import { useState, type FormEvent } from "react";
import { Mail } from "lucide-react";

import type { InvitationDelivery } from "@/lib/control-plane/client";
import {
  invitationOutcome,
  type InvitationOutcome,
} from "@/lib/members/invite-outcome";
import {
  MEMBER_ROLES,
  MEMBER_ROLE_HINTS,
  MEMBER_ROLE_LABELS,
} from "@/lib/members/roles";
import { Button } from "@/components/ui/button";
import { Field, Input, Label } from "@/components/ui/input";

// The invite surface, and the one place the acceptance link is ever shown.
//
// It is a client component for one reason: the raw acceptance token exists for
// exactly one response. The database stores only its hash, so if this request's
// body is not rendered the token is gone forever, and the token is
// bearer-equivalent so it cannot travel in a redirect URL. Reading it out of a
// fetch response and rendering it in place is the only way to put it in front of
// the person who issued it without writing it into browser history, a server
// log, or a cookie (issue #1440).
//
// The form still posts normally with JavaScript disabled. It degrades to the
// route's redirect shape, which reports the delivery outcome truthfully and
// simply cannot offer the link.

const SELECT_CLASSNAME = [
  "h-9 w-full rounded-md border border-[var(--color-border)]",
  "bg-[var(--color-surface)] px-2 text-sm text-[var(--color-ink)]",
  "transition-[border,box-shadow] duration-[var(--duration-fast)] ease-[var(--ease-out-expo)]",
  "focus-visible:outline-none focus-visible:border-[var(--color-accent)]",
  "focus-visible:ring-4 focus-visible:ring-[var(--color-accent-soft)]",
  "disabled:cursor-not-allowed disabled:opacity-50",
].join(" ");

interface InviteResponse {
  email: string;
  delivery: InvitationDelivery;
  link: string | null;
}

interface PanelState {
  outcome: InvitationOutcome;
  link: string | null;
}

async function issueInvitation(
  email: string,
  role: string,
): Promise<InviteResponse> {
  const response = await fetch("/api/console/members", {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      Accept: "application/json",
    },
    body: JSON.stringify({ email, role }),
  });

  const payload: unknown = await response.json().catch((): unknown => null);
  if (!response.ok) {
    const message =
      payload !== null &&
      typeof payload === "object" &&
      typeof (payload as { error?: unknown }).error === "string"
        ? (payload as { error: string }).error
        : "Could not create the invitation. Please try again.";
    throw new Error(message);
  }
  if (payload === null || typeof payload !== "object") {
    // An unreadable success body tells us nothing about delivery, so it is
    // reported as a failure rather than assumed to be a send.
    return { email, delivery: "failed", link: null };
  }
  const body = payload as Partial<InviteResponse>;
  return {
    email: typeof body.email === "string" ? body.email : email,
    delivery:
      body.delivery === "sent" ||
      body.delivery === "not_configured" ||
      body.delivery === "failed"
        ? body.delivery
        : "failed",
    link: typeof body.link === "string" ? body.link : null,
  };
}

export function InviteTeammateForm() {
  const router = useRouter();
  const [email, setEmail] = useState("");
  const [role, setRole] = useState("member");
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  const [state, setState] = useState<PanelState | null>(null);

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setBusy(true);
    setFailure(null);
    setState(null);
    try {
      const result = await issueInvitation(email, role);
      setState({
        outcome: invitationOutcome(result.delivery, result.email),
        link: result.link,
      });
      setEmail("");
      // Bring the new invitation into the table below without a full reload.
      router.refresh();
    } catch (err: unknown) {
      setFailure(
        err instanceof Error
          ? err.message
          : "Could not create the invitation. Please try again.",
      );
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="flex flex-col gap-4">
      <form
        method="POST"
        action="/api/console/members"
        onSubmit={handleSubmit}
        className="grid gap-3 sm:grid-cols-[1fr_auto_auto] sm:items-end"
      >
        <Field label="Email" htmlFor="invite-email" required>
          <Input
            id="invite-email"
            type="email"
            name="email"
            placeholder="teammate@example.com"
            value={email}
            onChange={(event) => setEmail(event.target.value)}
            required
          />
        </Field>
        <Field
          label="Role"
          htmlFor="invite-role"
          hint={MEMBER_ROLE_HINTS.member}
        >
          <select
            id="invite-role"
            name="role"
            value={role}
            onChange={(event) => setRole(event.target.value)}
            className={`${SELECT_CLASSNAME} sm:w-36`}
          >
            {MEMBER_ROLES.map((option) => (
              <option key={option} value={option}>
                {MEMBER_ROLE_LABELS[option]}
              </option>
            ))}
          </select>
        </Field>
        <Button type="submit" variant="primary" size="md" disabled={busy}>
          <Mail size={14} aria-hidden="true" />
          {busy ? "Creating…" : "Create invitation"}
        </Button>
      </form>

      {failure !== null ? (
        <p
          role="alert"
          className="rounded-lg border border-[var(--color-danger)] bg-[var(--color-surface)] px-4 py-3 text-sm text-[var(--color-danger)]"
        >
          {failure}
        </p>
      ) : null}

      {state !== null ? (
        <InvitationOutcomeNotice outcome={state.outcome} link={state.link} />
      ) : null}
    </div>
  );
}

// ResendInvitationButton reissues an outstanding invitation and shows the new
// link. Reissuing supersedes the previous one server-side, so exactly one link
// for an address is ever live.
export function ResendInvitationButton({
  email,
  role,
}: {
  email: string;
  role: string;
}) {
  const router = useRouter();
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  const [state, setState] = useState<PanelState | null>(null);

  async function handleClick() {
    setBusy(true);
    setFailure(null);
    setState(null);
    try {
      const result = await issueInvitation(email, role);
      setState({
        outcome: invitationOutcome(result.delivery, result.email),
        link: result.link,
      });
      router.refresh();
    } catch (err: unknown) {
      setFailure(
        err instanceof Error ? err.message : "Could not reissue the invitation.",
      );
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="flex flex-col gap-2">
      <Button
        type="button"
        variant="secondary"
        size="sm"
        disabled={busy}
        onClick={handleClick}
      >
        {busy ? "Working…" : "New link"}
      </Button>
      {failure !== null ? (
        <p role="alert" className="text-2xs text-[var(--color-danger)]">
          {failure}
        </p>
      ) : null}
      {state !== null ? (
        <InvitationOutcomeNotice outcome={state.outcome} link={state.link} compact />
      ) : null}
    </div>
  );
}

function InvitationOutcomeNotice({
  outcome,
  link,
  compact = false,
}: {
  outcome: InvitationOutcome;
  link: string | null;
  compact?: boolean;
}) {
  const border =
    outcome.tone === "success"
      ? "border-[var(--color-success)] text-[var(--color-success)]"
      : "border-[var(--color-warning)] text-[var(--color-warning)]";

  return (
    <div
      role="status"
      className={`flex flex-col gap-3 rounded-lg border ${border} bg-[var(--color-surface)] px-4 py-3 ${
        compact ? "text-2xs" : "text-sm"
      }`}
    >
      <p>
        {outcome.message}
        {outcome.action !== null ? ` ${outcome.action}` : null}
      </p>
      {link !== null ? <InvitationLink link={link} /> : null}
    </div>
  );
}

function InvitationLink({ link }: { link: string }) {
  const [copied, setCopied] = useState(false);

  async function copy() {
    try {
      await navigator.clipboard.writeText(link);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 2000);
    } catch {
      // A denied clipboard permission is not an error worth shouting about:
      // the link is on screen and selectable, which is the fallback.
      setCopied(false);
    }
  }

  return (
    <div className="flex flex-col gap-2">
      <Label htmlFor="invitation-link" className="text-[var(--color-ink-2)]">
        Invitation link
      </Label>
      <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
        <Input
          id="invitation-link"
          readOnly
          value={link}
          onFocus={(event) => event.currentTarget.select()}
          className="font-mono text-2xs"
        />
        <Button type="button" variant="secondary" size="sm" onClick={copy}>
          {copied ? "Copied" : "Copy"}
        </Button>
      </div>
      <p className="text-2xs text-[var(--color-ink-3)]">
        Anyone holding this link can join the workspace as the invited address,
        so send it the way you would send a password. It is shown once. Use New
        link on the row below to issue a fresh one, which retires this one.
      </p>
    </div>
  );
}

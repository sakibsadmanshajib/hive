"use client";

import { useRouter } from "next/navigation";
import {
  createContext,
  useCallback,
  useContext,
  useId,
  useRef,
  useState,
  type FormEvent,
  type ReactNode,
} from "react";
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
// Client, for one reason: the raw acceptance token exists for exactly one
// response. The database stores only its hash, so if this request's body is not
// rendered the token is gone forever, and the token is sensitive so it cannot
// travel in a redirect URL. Reading it out of a fetch response and rendering it
// in place is the only way to put it in front of the person who issued it
// without writing it into browser history, a server log, or a cookie
// (issue #1440).
//
// The outcome lives in a provider above the members table rather than in the
// control that produced it. That is not tidiness. `router.refresh()` runs after
// every issue so the table picks up the new invitation, and DataTable keys each
// row on its row key: anything that changes a row's identity remounts it and
// takes its state with it. A one-time link that lives inside a table row is a
// one-time link the interface can destroy before the user has read it, which
// would be a worse failure than the silent one this change exists to fix. Above
// the table, no row lifecycle can reach it.
//
// The form still posts normally with JavaScript disabled. It degrades to the
// route's redirect shape, which reports the delivery outcome truthfully and
// simply cannot carry a link.

const SELECT_CLASSNAME = [
  "h-9 w-full rounded-md border border-[var(--color-border)]",
  "bg-[var(--color-surface)] px-2 text-sm text-[var(--color-ink)]",
  "transition-[border,box-shadow] duration-[var(--duration-fast)] ease-[var(--ease-out-expo)]",
  "focus-visible:outline-none focus-visible:border-[var(--color-accent)]",
  "focus-visible:ring-4 focus-visible:ring-[var(--color-accent-soft)]",
  "disabled:cursor-not-allowed disabled:opacity-50",
].join(" ");

const GENERIC_FAILURE = "Could not create the invitation. Please try again.";

interface InviteResponse {
  email: string;
  delivery: InvitationDelivery;
  link: string | null;
}

interface PanelState {
  outcome: InvitationOutcome;
  link: string | null;
}

interface InvitePanelContextValue {
  state: PanelState | null;
  failure: string | null;
  // The address currently being issued, so exactly the control that was pressed
  // shows its own pending state.
  pending: string | null;
  issue: (email: string, role: string) => Promise<void>;
}

const InvitePanelContext = createContext<InvitePanelContextValue | null>(null);

function useInvitePanel(): InvitePanelContextValue {
  const value = useContext(InvitePanelContext);
  if (value === null) {
    // Deliberately loud. A silent no-op default would let the notice quietly
    // stop rendering if the provider were ever dropped from the page, which is
    // the exact class of failure this component exists to remove.
    throw new Error("useInvitePanel used outside InvitePanelProvider");
  }
  return value;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function readString(source: Record<string, unknown>, key: string): string | null {
  const value = source[key];
  return typeof value === "string" ? value : null;
}

function readDelivery(source: Record<string, unknown>): InvitationDelivery {
  const value = source.delivery;
  if (value === "sent" || value === "not_configured" || value === "failed") {
    return value;
  }
  // Never the happy path. An unrecognised value means the response is not
  // trustworthy, and "sent" is the claim that has to earn trust.
  return "failed";
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
    const message = isRecord(payload) ? readString(payload, "error") : null;
    throw new Error(message ?? GENERIC_FAILURE);
  }
  if (!isRecord(payload)) {
    // An unreadable success body says nothing about delivery, so it is reported
    // as a failure rather than assumed to be a send.
    return { email, delivery: "failed", link: null };
  }
  return {
    email: readString(payload, "email") ?? email,
    delivery: readDelivery(payload),
    link: readString(payload, "link"),
  };
}

export function InvitePanelProvider({ children }: { children: ReactNode }) {
  const router = useRouter();
  const [state, setState] = useState<PanelState | null>(null);
  const [failure, setFailure] = useState<string | null>(null);
  const [pending, setPending] = useState<string | null>(null);
  // A ref rather than the pending state, because two clicks can land in the
  // same React batch and both read the old state. Issuing twice would mint a
  // second token and retire the first, so the link the user just copied would
  // stop working.
  const inFlight = useRef(false);

  const issue = useCallback(
    async (email: string, role: string) => {
      if (inFlight.current) return;
      inFlight.current = true;
      setPending(email);
      setFailure(null);
      setState(null);
      try {
        const result = await issueInvitation(email, role);
        setState({
          outcome: invitationOutcome(result.delivery, result.email),
          link: result.link,
        });
        // Brings the new invitation into the table below. Safe for the link,
        // which lives here rather than in a row.
        router.refresh();
      } catch (err: unknown) {
        setFailure(err instanceof Error ? err.message : GENERIC_FAILURE);
      } finally {
        inFlight.current = false;
        setPending(null);
      }
    },
    [router],
  );

  return (
    <InvitePanelContext.Provider value={{ state, failure, pending, issue }}>
      {children}
    </InvitePanelContext.Provider>
  );
}

export function InviteTeammateForm() {
  const { issue, pending } = useInvitePanel();
  const [email, setEmail] = useState("");
  const [role, setRole] = useState("member");
  const busy = pending !== null;

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const address = email.trim();
    if (address === "") return;
    await issue(address, role);
    setEmail("");
  }

  return (
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
      <Field label="Role" htmlFor="invite-role" hint={MEMBER_ROLE_HINTS.member}>
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
        {busy ? "Working…" : "Create invitation"}
      </Button>
    </form>
  );
}

// ResendInvitationButton reissues an outstanding invitation. The new link
// appears in the notice above the table, not in this row, so the row can be
// remounted by the refresh that follows without taking the link with it.
export function ResendInvitationButton({
  email,
  role,
}: {
  email: string;
  role: string;
}) {
  const { issue, pending } = useInvitePanel();
  const busy = pending !== null;

  return (
    <Button
      type="button"
      variant="secondary"
      size="sm"
      disabled={busy}
      onClick={() => {
        void issue(email, role);
      }}
    >
      {pending === email ? "Working…" : "New link"}
    </Button>
  );
}

// InvitationOutcomeNotice renders the single outcome for the whole page.
export function InvitationOutcomeNotice() {
  const { state, failure } = useInvitePanel();

  if (failure !== null) {
    return (
      <p
        role="alert"
        className="rounded-lg border border-[var(--color-danger)] bg-[var(--color-surface)] px-4 py-3 text-sm text-[var(--color-danger)]"
      >
        {failure}
      </p>
    );
  }
  if (state === null) return null;

  const border =
    state.outcome.tone === "success"
      ? "border-[var(--color-success)] text-[var(--color-success)]"
      : "border-[var(--color-warning)] text-[var(--color-warning)]";

  return (
    <div
      role="status"
      className={`flex flex-col gap-3 rounded-lg border ${border} bg-[var(--color-surface)] px-4 py-3 text-sm`}
    >
      <p>
        {state.outcome.message}
        {state.outcome.action !== null ? ` ${state.outcome.action}` : null}
      </p>
      {state.link !== null ? <InvitationLink link={state.link} /> : null}
    </div>
  );
}

function InvitationLink({ link }: { link: string }) {
  // Generated rather than hardcoded. A fixed id would collide with any other
  // instance on the page and silently break the label association for a screen
  // reader.
  const inputId = useId();
  const [copied, setCopied] = useState(false);

  async function copy() {
    try {
      await navigator.clipboard.writeText(link);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 2000);
    } catch {
      // A denied clipboard permission is not worth shouting about: the link is
      // on screen and selectable, which is the fallback.
      setCopied(false);
    }
  }

  return (
    <div className="flex flex-col gap-2">
      <Label htmlFor={inputId} className="text-[var(--color-ink-2)]">
        Invitation link
      </Label>
      <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
        <Input
          id={inputId}
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
        This link is shown once. Whoever opens it joins the workspace as the
        invited address, and only that address, so treat it as private. Use New
        link on the row below to issue a fresh one, which retires this one.
      </p>
    </div>
  );
}

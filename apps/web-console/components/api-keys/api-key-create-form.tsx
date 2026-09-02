"use client";

import { useRef, useState, type FormEvent } from "react";
import { useRouter } from "next/navigation";
import { Copy, Plus, Check } from "lucide-react";

import type { ApiKey } from "@/lib/control-plane/client";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Field, Input } from "@/components/ui/input";
import { formatShortDate } from "@/lib/format/credits";
import { formatUsdFromCredits } from "@/lib/format/model-pricing";
import { MAX_KEY_NICKNAME_LEN, usdToCreditsInput } from "@/lib/api-keys";
import { buildQuickstartCurl } from "@/lib/quickstart-model";

// Mirrors UpdateApiKeyBudgetInput["budgetKind"] in lib/control-plane/client.ts.
// Duplicated as a literal union rather than imported: that type lives in a
// server-only module (getRequestContext, cookies) this client component must
// not pull in.
type ResetCadence = "never" | "monthly";

// The cadence is not decoration on the amount, it changes what the amount
// means, so it is spelled out on the amount field itself. Before this the
// cadence select produced no visible consequence at all: a customer picked
// "Every month", nothing on screen acknowledged it, and the interaction
// coverage gate reported the control as having no proven effect, which was an
// accurate reading of a money control that gave no feedback.
const LIMIT_HINT: Record<ResetCadence, string> = {
  never: "Total for the key's lifetime; blank for unlimited",
  monthly: "Per calendar month; blank for unlimited",
};

const BUDGET_NOT_APPLIED =
  "Key created, but the credit limit could not be applied. The key is live and uncapped. Set the limit from the key's settings.";

const CADENCE_PHRASE: Record<ResetCadence, string> = {
  never: "spent in total",
  monthly: "spent in the current calendar month",
};

/**
 * The exact bound this form is about to ask the control-plane to enforce.
 *
 * Worded against what edge-api actually does rather than what the field is
 * called: `authz.CheckAccess` refuses a request when
 * `consumed + reserved + estimated > budget_limit_credits`, so the limit is
 * the point a request is refused for crossing, not a figure the key is
 * allowed to reach and then stop at. Saying "stops at $10.00" would promise a
 * different bound from the one the server enforces.
 *
 * A blank field says the cap is absent in the same sentence, because an
 * unstated absence is how a customer ends up believing an uncapped key is
 * capped.
 */
export function limitSummaryText(rawLimit: string, cadence: ResetCadence): string {
  if (rawLimit.trim() === "") {
    return "No credit limit: this key can spend the account balance.";
  }
  const credits = usdToCreditsInput(rawLimit);
  if (credits === null) {
    return "Credit limit must be a positive dollar amount, so no limit will be applied.";
  }
  return `Enforced: a request is refused once it would push this key past ${formatUsdFromCredits(
    credits,
  )} ${CADENCE_PHRASE[cadence]}.`;
}

interface CreateApiKeyResponse {
  id: string;
  nickname: string;
  status: string;
  redacted_suffix: string;
  created_at: string;
  updated_at: string;
  expires_at: string | null;
  last_used_at: string | null;
  expiration_summary: { kind: string; label: string };
  budget_summary: { kind: string; label: string };
  allowlist_summary: { mode: string; group_names: string[]; label: string };
  spend_credits: number;
  budget_limit_credits: number | null;
  // Mirrors ApiKey in lib/control-plane/client.ts. A freshly minted key has no
  // cap, so the control-plane sends null here; the field is declared because
  // this response is handed straight to state typed as ApiKey.
  budget_spend_credits: number | null;
  secret?: string;
}

const CREATE_FAILED = "Failed to create key. Please try again.";

function isExplainedError(value: unknown): value is { error: string; code: string } {
  if (value === null || typeof value !== "object") return false;
  const candidate = value as Record<string, unknown>;
  return (
    typeof candidate.error === "string" &&
    candidate.error.trim() !== "" &&
    typeof candidate.code === "string" &&
    candidate.code.trim() !== ""
  );
}

/**
 * Show the server's sentence only when the proxy route named a machine code
 * alongside it. That pairing is the route's signal that the wording is its own
 * customer-facing copy for a refusal it recognised, such as the workspace with
 * no billing link that would have produced a key the API refuses (issue #1330).
 * Printing "please try again" over that sends the customer round a loop that
 * cannot succeed.
 *
 * Without a code the body carries only a status class ("Conflict", "Upstream
 * service error"), which tells a customer nothing, so those keep the generic
 * wording this form has always used.
 */
async function createKeyErrorMessage(response: Response): Promise<string> {
  const body: unknown = await response.json().catch(() => null);
  return isExplainedError(body) ? body.error : CREATE_FAILED;
}

function isApiKeyResponse(value: unknown): value is CreateApiKeyResponse {
  if (value === null || typeof value !== "object") return false;
  const candidate = value as Record<string, unknown>;
  return (
    typeof candidate.id === "string" &&
    typeof candidate.nickname === "string"
  );
}

export interface ApiKeyCreateFormProps {
  /**
   * Gateway base URL of THIS deployment, already resolved by
   * `resolveApiBaseUrl` on the server. Passed down rather than read here: the
   * value is operator configuration read at request time, and a Client
   * Component has no access to it (issue #550).
   */
  apiBaseUrl: string;
  /**
   * Model alias the sample request names, from `pickQuickstartAlias`, so the
   * command works on a fresh account instead of refusing on a credit hold
   * (issue #1372).
   */
  quickstartModel: string;
}

/** What the copy buttons most recently put on the clipboard. */
type CopiedTarget = "secret" | "command";

export function ApiKeyCreateForm({
  apiBaseUrl,
  quickstartModel,
}: ApiKeyCreateFormProps) {
  const [nickname, setNickname] = useState("");
  const [expiresAt, setExpiresAt] = useState("");
  const [creditLimit, setCreditLimit] = useState("");
  const [resetCadence, setResetCadence] = useState<ResetCadence>("never");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [createdKey, setCreatedKey] = useState<ApiKey | null>(null);
  const [appliedLimitCredits, setAppliedLimitCredits] = useState<number | null>(null);
  const [budgetWarning, setBudgetWarning] = useState<string | null>(null);
  const [copied, setCopied] = useState<CopiedTarget | null>(null);
  const copyResetTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const router = useRouter();

  async function handleSubmit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const trimmedNickname = nickname.trim();
    if (!trimmedNickname) {
      setError("Nickname is required.");
      return;
    }
    // The maxLength attribute stops typing past the cap, but a paste handler
    // or a programmatic fill can still get past it, and the control plane is
    // the boundary that actually refuses the value (issue #1400).
    if ([...trimmedNickname].length > MAX_KEY_NICKNAME_LEN) {
      setError(`Nickname must be ${MAX_KEY_NICKNAME_LEN} characters or fewer.`);
      return;
    }
    if (expiresAt && new Date(expiresAt).getTime() <= Date.now()) {
      setError("Expiry must be a date in the future.");
      return;
    }
    const limitCredits = usdToCreditsInput(creditLimit);
    if (creditLimit.trim() !== "" && limitCredits === null) {
      setError("Credit limit must be a positive dollar amount.");
      return;
    }

    setLoading(true);
    setError(null);
    setBudgetWarning(null);

    try {
      const body: { nickname: string; expires_at?: string } = {
        nickname: trimmedNickname,
      };
      if (expiresAt) {
        body.expires_at = new Date(expiresAt).toISOString();
      }

      const response = await fetch("/api/v1/accounts/current/api-keys", {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
      });

      if (!response.ok) {
        setError(await createKeyErrorMessage(response));
        setLoading(false);
        return;
      }

      const data: unknown = await response.json();
      if (!isApiKeyResponse(data)) {
        setError(CREATE_FAILED);
        setLoading(false);
        return;
      }

      // The credit cap is a second call against the just-created key's own
      // policy endpoint (POST .../api-keys/{id}/policy), not part of key
      // creation itself -- that endpoint is also what edge-api's
      // authz.CheckAccess enforces against on every request, so a failure
      // here means the key exists with NO cap, not a cap that silently
      // failed to apply. That must be surfaced, not swallowed: a customer
      // who believes a limit is in place when none was ever set is worse off
      // than one who was told it did not apply.
      if (limitCredits !== null) {
        // From here the key exists, and its secret is shown exactly once. A
        // throw inside this block would land in the outer catch, which reports
        // "failed to create key" and never renders the secret panel: the
        // customer would be told nothing was created while holding a real,
        // uncapped key whose only copy of the secret had just been discarded.
        // So a transport failure is caught here and reported as the same
        // "limit not applied" warning a refusal produces, and the panel still
        // renders.
        try {
          const policyResponse = await fetch(
            `/api/v1/accounts/current/api-keys/${data.id}/policy`,
            {
              method: "POST",
              credentials: "include",
              headers: { "Content-Type": "application/json" },
              body: JSON.stringify({
                budget_kind: resetCadence === "monthly" ? "monthly" : "lifetime",
                budget_limit_credits: limitCredits,
              }),
            },
          );
          if (policyResponse.ok) {
            setAppliedLimitCredits(limitCredits);
          } else {
            setBudgetWarning(BUDGET_NOT_APPLIED);
          }
        } catch {
          setBudgetWarning(BUDGET_NOT_APPLIED);
        }
      }

      setCreatedKey(data);
      router.refresh();
    } catch (err: unknown) {
      const message =
        err instanceof Error ? err.message : "Failed to create key.";
      setError(message);
    } finally {
      setLoading(false);
    }
  }

  async function handleCopy(target: CopiedTarget, text: string) {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(target);
      // One timer, cancelled and restarted per click. Without the cancel, two
      // copy buttons share one deadline: copying the command a second after
      // copying the key would show "Copied" for a second rather than two,
      // because the key's timer is still pending and clears it.
      if (copyResetTimer.current !== null) {
        clearTimeout(copyResetTimer.current);
      }
      copyResetTimer.current = setTimeout(() => setCopied(null), 2000);
    } catch {
      // Clipboard API unavailable — user must copy manually.
    }
  }

  if (createdKey) {
    // The command is built only while the plaintext secret is in hand. Issue
    // #550 asked for a runnable example, and the whole value of it is that it
    // runs as rendered: substituting a placeholder when the secret is missing
    // would produce a command that looks copy-and-run and is not, which is a
    // worse first impression than saying plainly that there is nothing to show.
    // The secret is optional on the wire (`secret?: string`), so this is a real
    // branch, not a defensive one.
    const quickstartCurl = createdKey.secret
      ? buildQuickstartCurl({
          baseUrl: apiBaseUrl,
          model: quickstartModel,
          credential: createdKey.secret,
        })
      : null;

    return (
      <Card>
        <CardHeader>
          <CardTitle>Key created — copy it now</CardTitle>
          <CardDescription>
            This is the only time the secret is shown. Store it in a secret
            manager before navigating away.
          </CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-4 px-5 py-5">
          <div className="flex items-center gap-2 rounded-md border border-[var(--color-border)] bg-[var(--color-surface-inset)] px-3 py-2">
            <code
              className="flex-1 overflow-x-auto whitespace-nowrap font-mono text-xs text-[var(--color-ink)]"
              data-testid="created-api-key-secret"
            >
              {createdKey.secret ?? "—"}
            </code>
            {/* No secret means nothing to copy, and a button that silently
                does nothing is worse than an absent one. */}
            {createdKey.secret ? (
              <Button
                type="button"
                variant="secondary"
                size="sm"
                onClick={() => void handleCopy("secret", createdKey.secret ?? "")}
              >
                {copied === "secret" ? (
                  <>
                    <Check size={14} aria-hidden="true" /> Copied
                  </>
                ) : (
                  <>
                    <Copy size={14} aria-hidden="true" /> Copy
                  </>
                )}
              </Button>
            ) : null}
          </div>
          <dl className="grid grid-cols-2 gap-2 text-xs text-[var(--color-ink-3)]">
            <div className="flex flex-col gap-0.5">
              <dt className="text-2xs uppercase tracking-wider">Nickname</dt>
              <dd className="text-sm text-[var(--color-ink)]">
                {createdKey.nickname}
              </dd>
            </div>
            <div className="flex flex-col gap-0.5">
              <dt className="text-2xs uppercase tracking-wider">Expires</dt>
              <dd className="text-sm text-[var(--color-ink)] tabular-nums">
                {createdKey.expires_at
                  ? formatShortDate(createdKey.expires_at)
                  : "Never"}
              </dd>
            </div>
            <div className="flex flex-col gap-0.5">
              <dt className="text-2xs uppercase tracking-wider">
                Credit limit
              </dt>
              <dd
                className="text-sm text-[var(--color-ink)] tabular-nums"
                data-testid="created-api-key-limit"
              >
                {appliedLimitCredits !== null
                  ? `${formatUsdFromCredits(appliedLimitCredits)} (${
                      resetCadence === "monthly" ? "resets monthly" : "never resets"
                    })`
                  : "Unlimited"}
              </dd>
            </div>
          </dl>
          {budgetWarning ? (
            <p role="alert" className="text-xs text-[var(--color-danger)]">
              {budgetWarning}
            </p>
          ) : null}

          {/*
            Issue #550. The panel used to end at the secret, so the next step
            after minting a key was to leave the product and guess a hostname.
            This is an OpenAI-compatible gateway: the one thing this screen owes
            a developer is a request that works, at the only moment the key
            exists in plaintext.
          */}
          <section className="flex flex-col gap-3 border-t border-[var(--color-border)] pt-4">
            <div className="flex flex-col gap-0.5">
              {/* h4, because CardTitle above renders the card's own h3. */}
              <h4 className="text-sm font-medium text-[var(--color-ink)]">
                Send your first request
              </h4>
              <p className="text-xs text-[var(--color-ink-3)]">
                Any OpenAI SDK works against this base URL. Swap the key, keep
                the code.
              </p>
            </div>

            {/* Same dl/dt/dd shape as the key's own facts above, and as the
                quickstart on /console/docs, so a screen reader reads this as
                the labelled value it is rather than as loose text. */}
            <dl className="flex flex-col gap-1">
              <dt className="text-2xs uppercase tracking-wider text-[var(--color-ink-3)]">
                Base URL
              </dt>
              <dd
                className="overflow-x-auto whitespace-nowrap font-mono text-xs text-[var(--color-ink)]"
                data-testid="created-api-key-base-url"
              >
                {apiBaseUrl}
              </dd>
            </dl>

            {quickstartCurl ? (
              <div className="flex flex-col gap-2">
                {/* tabIndex 0 and a group role: the block scrolls sideways, and
                    a scrollable region that cannot be focused cannot be
                    scrolled by keyboard at all. */}
                <pre
                  tabIndex={0}
                  role="group"
                  aria-label="Sample request using this key"
                  className="overflow-x-auto rounded-md border border-[var(--color-border)] bg-[var(--color-surface-inset)] px-3 py-3 text-xs leading-relaxed text-[var(--color-ink-2)]"
                >
                  <code
                    className="font-mono"
                    data-testid="created-api-key-curl"
                  >
                    {quickstartCurl}
                  </code>
                </pre>
                <div className="flex items-center gap-3">
                  <Button
                    type="button"
                    variant="secondary"
                    size="sm"
                    onClick={() => void handleCopy("command", quickstartCurl)}
                  >
                    {copied === "command" ? (
                      <>
                        <Check size={14} aria-hidden="true" /> Copied
                      </>
                    ) : (
                      <>
                        <Copy size={14} aria-hidden="true" /> Copy command
                      </>
                    )}
                  </Button>
                  <p className="text-xs text-[var(--color-ink-3)]">
                    This command carries the key above. Treat it like the key
                    itself.
                  </p>
                </div>
              </div>
            ) : (
              <p
                className="text-xs text-[var(--color-ink-3)]"
                data-testid="created-api-key-quickstart-note"
              >
                The secret was not returned with this response, so there is no
                ready-to-run command to show. Create a replacement key to get
                one.
              </p>
            )}
          </section>

          <Button
            type="button"
            variant="secondary"
            size="sm"
            onClick={() => {
              setCreatedKey(null);
              setNickname("");
              setExpiresAt("");
              setCreditLimit("");
              setResetCadence("never");
              setAppliedLimitCredits(null);
              setBudgetWarning(null);
              setCopied(null);
            }}
            className="self-start"
          >
            Create another
          </Button>
        </CardContent>
      </Card>
    );
  }

  const limitSummary = limitSummaryText(creditLimit, resetCadence);

  return (
    <Card>
      <CardHeader>
        <CardTitle>Create API key</CardTitle>
        <CardDescription>
          Each key is scoped to this workspace. Set an expiry to enforce
          rotation cadence.
        </CardDescription>
      </CardHeader>
      <CardContent className="px-5 py-5">
        <form
          onSubmit={(e) => void handleSubmit(e)}
          className="grid gap-4 sm:grid-cols-[1fr_220px_auto] sm:items-end"
        >
          <Field label="Nickname" htmlFor="key-nickname" required>
            <Input
              id="key-nickname"
              type="text"
              value={nickname}
              onChange={(e) => setNickname(e.target.value)}
              placeholder="production-server"
              maxLength={MAX_KEY_NICKNAME_LEN}
              required
            />
          </Field>
          <Field
            label="Expires"
            htmlFor="key-expires"
            hint="Optional"
          >
            <Input
              id="key-expires"
              type="date"
              value={expiresAt}
              onChange={(e) => setExpiresAt(e.target.value)}
            />
          </Field>
          <div aria-hidden="true" className="hidden sm:block" />
          <Field
            label="Credit limit (USD)"
            htmlFor="key-credit-limit"
            hint={LIMIT_HINT[resetCadence]}
          >
            <Input
              id="key-credit-limit"
              type="text"
              inputMode="decimal"
              value={creditLimit}
              onChange={(e) => setCreditLimit(e.target.value)}
              placeholder="e.g. 10.00"
            />
          </Field>
          <Field
            label="Reset limit every…"
            htmlFor="key-reset-cadence"
            hint="Only applies when a credit limit is set"
          >
            <select
              id="key-reset-cadence"
              value={resetCadence}
              onChange={(e) => setResetCadence(e.target.value as ResetCadence)}
              className="flex h-9 w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 text-sm text-[var(--color-ink)]"
            >
              <option value="never">Never (lifetime cap)</option>
              <option value="monthly">Every month</option>
            </select>
          </Field>
          <p
            className="text-xs text-[var(--color-ink-3)] sm:col-span-3"
            data-testid="key-limit-summary"
          >
            {limitSummary}
          </p>
          <Button
            type="submit"
            variant="primary"
            disabled={loading}
            className="sm:self-end"
          >
            <Plus size={14} aria-hidden="true" />
            {loading ? "Creating…" : "Create key"}
          </Button>
          {error ? (
            <p
              role="alert"
              className="text-xs text-[var(--color-danger)] sm:col-span-3"
            >
              {error}
            </p>
          ) : null}
        </form>
      </CardContent>
    </Card>
  );
}

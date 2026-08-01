"use client";

import { useState, type FormEvent, type ReactElement } from "react";
import {
  TIER_NAMES,
  validateLimits,
  type KeyLimits,
  type KeyLimitsInput,
  type SaveLimitsResult,
  type TierLimit,
  type TierName,
  type TierOverrides,
} from "@/lib/api-keys";

interface RateLimitFormProps {
  initial: KeyLimits;
  canEdit: boolean;
  // Server action supplied by the page. Issue #552: this used to be a fetch
  // client, which is neither serialisable across the Server/Client boundary
  // nor able to resolve the control-plane origin from the browser.
  onSave: (input: KeyLimitsInput) => Promise<SaveLimitsResult>;
}

interface FormState {
  rpm: number;
  tpm: number;
  tierOverrides: TierOverrides;
}

function initialState(initial: KeyLimits): FormState {
  return {
    rpm: initial.rpm,
    tpm: initial.tpm,
    tierOverrides: { ...initial.tier_overrides },
  };
}

function setTier(
  overrides: TierOverrides,
  tier: TierName,
  patch: Partial<TierLimit>,
): TierOverrides {
  const next: TierOverrides = { ...overrides };
  const existing: TierLimit = next[tier] ?? { rpm: 0, tpm: 0 };
  next[tier] = { rpm: patch.rpm ?? existing.rpm, tpm: patch.tpm ?? existing.tpm };
  return next;
}

function clearTier(overrides: TierOverrides, tier: TierName): TierOverrides {
  const next: TierOverrides = { ...overrides };
  delete next[tier];
  return next;
}

export function RateLimitForm({ initial, canEdit, onSave }: RateLimitFormProps): ReactElement {
  const [state, setState] = useState<FormState>(() => initialState(initial));
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [savedAt, setSavedAt] = useState<number | null>(null);

  async function onSubmit(event: FormEvent<HTMLFormElement>): Promise<void> {
    event.preventDefault();
    setError(null);
    const input: KeyLimitsInput = {
      rpm: state.rpm,
      tpm: state.tpm,
      tier_overrides: state.tierOverrides,
    };
    const validationErr = validateLimits(input);
    if (validationErr !== null) {
      setError(validationErr);
      return;
    }
    setSubmitting(true);
    try {
      const result = await onSave(input);
      if (result.ok) {
        setSavedAt(Date.now());
      } else {
        setError(result.error);
      }
    } catch {
      // A thrown action means the request never completed (network drop, or a
      // redacted server error). Nothing was saved, so say only that.
      setError("Could not save the rate limits. Please try again.");
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <form onSubmit={onSubmit} aria-label="rate-limit-form" data-testid="rate-limit-form">
      <fieldset disabled={!canEdit || submitting}>
        <legend>Per-key limits</legend>
        <div>
          <label htmlFor="rl-rpm">Requests per minute</label>
          <input
            id="rl-rpm"
            name="rpm"
            type="number"
            min={0}
            max={100000}
            value={state.rpm}
            onChange={(e) =>
              setState((prev) => ({ ...prev, rpm: Number(e.target.value) }))
            }
            data-testid="rl-rpm"
          />
        </div>
        <div>
          <label htmlFor="rl-tpm">Tokens per minute</label>
          <input
            id="rl-tpm"
            name="tpm"
            type="number"
            min={0}
            max={10000000}
            value={state.tpm}
            onChange={(e) =>
              setState((prev) => ({ ...prev, tpm: Number(e.target.value) }))
            }
            data-testid="rl-tpm"
          />
        </div>

        <h3>Tier overrides</h3>
        <p>Leave a tier blank to use the system default.</p>
        {TIER_NAMES.map((tier: TierName) => {
          const current: TierLimit = state.tierOverrides[tier] ?? { rpm: 0, tpm: 0 };
          const enabled = state.tierOverrides[tier] !== undefined;
          return (
            <div key={tier} data-testid={`tier-row-${tier}`}>
              <label>
                <input
                  type="checkbox"
                  checked={enabled}
                  onChange={(e) =>
                    setState((prev) => ({
                      ...prev,
                      tierOverrides: e.target.checked
                        ? setTier(prev.tierOverrides, tier, current)
                        : clearTier(prev.tierOverrides, tier),
                    }))
                  }
                  data-testid={`tier-toggle-${tier}`}
                />
                Override <strong>{tier}</strong>
              </label>
              <input
                type="number"
                min={0}
                max={100000}
                disabled={!enabled}
                value={current.rpm}
                onChange={(e) =>
                  setState((prev) => ({
                    ...prev,
                    tierOverrides: setTier(prev.tierOverrides, tier, {
                      rpm: Number(e.target.value),
                    }),
                  }))
                }
                aria-label={`${tier} RPM`}
                data-testid={`tier-rpm-${tier}`}
              />
              <input
                type="number"
                min={0}
                max={10000000}
                disabled={!enabled}
                value={current.tpm}
                onChange={(e) =>
                  setState((prev) => ({
                    ...prev,
                    tierOverrides: setTier(prev.tierOverrides, tier, {
                      tpm: Number(e.target.value),
                    }),
                  }))
                }
                aria-label={`${tier} TPM`}
                data-testid={`tier-tpm-${tier}`}
              />
            </div>
          );
        })}

        {error !== null ? (
          <p role="alert" data-testid="rl-error">
            {error}
          </p>
        ) : null}
        {savedAt !== null && error === null ? (
          <p role="status" data-testid="rl-saved">
            Saved.
          </p>
        ) : null}

        <button type="submit" disabled={!canEdit || submitting} data-testid="rl-submit">
          {submitting ? "Saving…" : "Save"}
        </button>
      </fieldset>
    </form>
  );
}

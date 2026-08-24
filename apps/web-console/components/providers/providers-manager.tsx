"use client";

import * as React from "react";
import { AlertCircle } from "lucide-react";

import { cn } from "@/lib/cn";
import type { CustomProvider, UpsertProviderInput } from "@/lib/control-plane/client";

interface ProvidersManagerProps {
  providers: CustomProvider[];
}

type RowStatus = "idle" | "saving" | "error";

// providerPutBody builds the full-replace body for one provider row. The
// upstream PUT has no partial update, so every field must be echoed back:
// a body missing litellm_prefix would silently wipe it server-side. Both the
// edit form and the enabled toggle route through this builder so there is
// exactly one place that knows the field set (guarded by
// providers-manager.test.ts).
export function providerPutBody(
  p: Pick<
    CustomProvider,
    "slug" | "display_name" | "base_url" | "api_key_env" | "litellm_prefix"
  >,
  enabled: boolean,
): UpsertProviderInput {
  return {
    slug: p.slug.trim(),
    display_name: p.display_name.trim(),
    base_url: p.base_url.trim(),
    api_key_env: p.api_key_env.trim(),
    litellm_prefix: p.litellm_prefix.trim(),
    enabled,
  };
}

export function ProvidersManager({ providers: initialProviders }: ProvidersManagerProps) {
  const [providers, setProviders] = React.useState<CustomProvider[]>(initialProviders);
  const [status, setStatus] = React.useState<Record<string, RowStatus>>({});
  const [formError, setFormError] = React.useState<string | null>(null);
  const [submitting, setSubmitting] = React.useState(false);
  const [editingId, setEditingId] = React.useState<string | null>(null);
  const [editError, setEditError] = React.useState<string | null>(null);

  function setRowStatus(id: string, next: RowStatus): void {
    setStatus((prev) => ({ ...prev, [id]: next }));
  }

  async function handleCreate(event: React.FormEvent<HTMLFormElement>): Promise<void> {
    event.preventDefault();
    setFormError(null);

    // Captured synchronously: a native Event's currentTarget is only valid
    // during dispatch, so it must not be read again after an await below.
    const formEl = event.currentTarget;
    const form = new FormData(formEl);
    const slug = String(form.get("slug") ?? "").trim();
    const displayName = String(form.get("display_name") ?? "").trim();
    const baseUrl = String(form.get("base_url") ?? "").trim();
    const apiKeyEnv = String(form.get("api_key_env") ?? "").trim();
    const litellmPrefix = String(form.get("litellm_prefix") ?? "").trim();

    if (slug === "") {
      setFormError("A slug is required.");
      return;
    }
    if (apiKeyEnv === "") {
      setFormError("An API key environment variable name is required.");
      return;
    }

    setSubmitting(true);
    try {
      const response = await fetch("/api/console/providers", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          slug,
          display_name: displayName,
          base_url: baseUrl,
          api_key_env: apiKeyEnv,
          litellm_prefix: litellmPrefix,
          enabled: true,
        }),
      });
      if (!response.ok) {
        const body: { error?: string } = await response.json().catch(() => ({}));
        setFormError(body.error ?? "Could not register the provider.");
        return;
      }
      const created: CustomProvider = await response.json();
      setProviders((prev) => [...prev, created]);
      formEl.reset();
    } catch {
      setFormError("Could not register the provider. Please try again.");
    } finally {
      setSubmitting(false);
    }
  }

  async function handleSave(
    event: React.FormEvent<HTMLFormElement>,
    original: CustomProvider,
  ): Promise<void> {
    event.preventDefault();
    setEditError(null);

    // Captured synchronously: a native Event's currentTarget is only valid
    // during dispatch, so it must not be read again after an await below.
    const formEl = event.currentTarget;
    const form = new FormData(formEl);
    const slug = String(form.get("slug") ?? "").trim();
    const displayName = String(form.get("display_name") ?? "").trim();
    const baseUrl = String(form.get("base_url") ?? "").trim();
    const apiKeyEnv = String(form.get("api_key_env") ?? "").trim();
    const litellmPrefix = String(form.get("litellm_prefix") ?? "").trim();

    if (slug === "") {
      setEditError("A slug is required.");
      return;
    }
    if (apiKeyEnv === "") {
      setEditError("An API key environment variable name is required.");
      return;
    }

    setRowStatus(original.id, "saving");
    try {
      const response = await fetch(`/api/console/providers/${encodeURIComponent(original.id)}`, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          slug,
          display_name: displayName,
          base_url: baseUrl,
          api_key_env: apiKeyEnv,
          litellm_prefix: litellmPrefix,
          enabled: original.enabled,
        }),
      });
      if (!response.ok) {
        const body: { error?: string } = await response.json().catch(() => ({}));
        setEditError(body.error ?? "Could not save the provider.");
        return;
      }
      const updated: CustomProvider = await response.json();
      setProviders((prev) => prev.map((p) => (p.id === updated.id ? updated : p)));
      setEditingId(null);
    } catch {
      setEditError("Could not save the provider. Please try again.");
    } finally {
      setRowStatus(original.id, "idle");
    }
  }

  async function toggle(provider: CustomProvider): Promise<void> {
    const next = !provider.enabled;

    // Optimistic flip, rolled back on failure. The body must carry every
    // field: the upstream PUT replaces the whole record.
    const optimistic: CustomProvider = { ...provider, enabled: next };
    setProviders((prev) => prev.map((p) => (p.id === provider.id ? optimistic : p)));
    setRowStatus(provider.id, "saving");

    try {
      const response = await fetch(`/api/console/providers/${encodeURIComponent(provider.id)}`, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(providerPutBody(provider, next)),
      });
      if (!response.ok) {
        throw new Error("request failed");
      }
      const updated: CustomProvider = await response.json();
      setProviders((prev) => prev.map((p) => (p.id === updated.id ? updated : p)));
      setRowStatus(provider.id, "idle");
    } catch {
      setProviders((prev) => prev.map((p) => (p.id === provider.id ? provider : p)));
      setRowStatus(provider.id, "error");
    }
  }

  return (
    <div className="flex flex-col gap-10">
      <form
        onSubmit={(event) => {
          void handleCreate(event);
        }}
        className="flex flex-col gap-3 rounded-lg border border-[var(--color-border)] bg-[var(--color-surface)] p-4"
      >
        <h2 className="text-2xs font-medium uppercase tracking-[0.14em] text-[var(--color-ink-3)]">
          Register a provider endpoint
        </h2>
        <div className="flex flex-wrap gap-3">
          <input
            name="slug"
            required
            placeholder="Slug (e.g. together-ai)"
            className="min-w-[140px] flex-1 rounded border border-[var(--color-border)] bg-transparent px-2 py-1.5 text-sm"
          />
          <input
            name="display_name"
            placeholder="Display name"
            className="min-w-[160px] flex-1 rounded border border-[var(--color-border)] bg-transparent px-2 py-1.5 text-sm"
          />
        </div>
        <input
          name="base_url"
          type="url"
          placeholder="Base URL (e.g. https://api.together.xyz/v1)"
          className="rounded border border-[var(--color-border)] bg-transparent px-2 py-1.5 text-sm"
        />
        <div className="flex flex-wrap gap-3">
          <input
            name="api_key_env"
            required
            placeholder="API key env var name (e.g. TOGETHER_API_KEY)"
            className="min-w-[220px] flex-1 rounded border border-[var(--color-border)] bg-transparent px-2 py-1.5 font-mono text-sm"
          />
          <input
            name="litellm_prefix"
            placeholder="LiteLLM prefix (optional, e.g. openrouter/)"
            className="min-w-[180px] flex-1 rounded border border-[var(--color-border)] bg-transparent px-2 py-1.5 font-mono text-sm"
          />
        </div>
        <p className="text-2xs text-[var(--color-ink-3)]">
          The key itself never passes through Hive: set the named environment
          variable on the gateway host, and the registry stores its name only.
        </p>
        {formError ? (
          <span className="flex items-center gap-1 text-2xs text-[var(--color-danger,#d64545)]">
            <AlertCircle size={12} />
            {formError}
          </span>
        ) : null}
        <button
          type="submit"
          disabled={submitting}
          className={cn(
            "self-start rounded bg-[var(--color-accent)] px-3 py-1.5 text-sm text-white",
            submitting ? "cursor-wait opacity-70" : "cursor-pointer",
          )}
        >
          {submitting ? "Registering…" : "Register provider"}
        </button>
      </form>

      {providers.length === 0 ? (
        <EmptyProviderList />
      ) : (
        <section className="flex flex-col gap-3">
          <h2 className="text-2xs font-medium uppercase tracking-[0.14em] text-[var(--color-ink-3)]">
            Registered endpoints ({providers.length})
          </h2>
          <ul className="flex flex-col rounded-lg border border-[var(--color-border)] bg-[var(--color-surface)] divide-y divide-[var(--color-border)]">
            {providers.map((provider) =>
              editingId === provider.id ? (
                <li key={provider.id} className="px-4 py-3.5">
                  <EditForm
                    provider={provider}
                    error={editError}
                    onCancel={() => {
                      setEditingId(null);
                      setEditError(null);
                    }}
                    onSave={(event) => {
                      void handleSave(event, provider);
                    }}
                  />
                </li>
              ) : (
                <ProviderRow
                  key={provider.id}
                  provider={provider}
                  status={status[provider.id] ?? "idle"}
                  onToggle={() => {
                    void toggle(provider);
                  }}
                  onEdit={() => {
                    setEditError(null);
                    setEditingId(provider.id);
                  }}
                />
              ),
            )}
          </ul>
        </section>
      )}
    </div>
  );
}

function EmptyProviderList() {
  return (
    <div className="rounded-lg border border-dashed border-[var(--color-border)] bg-[var(--color-surface)] px-6 py-10 text-center">
      <p className="text-sm font-semibold text-[var(--color-ink)]">No endpoints registered yet</p>
      <p className="mt-1 text-sm text-[var(--color-ink-3)]">
        Register an OpenAI-compatible base URL above so routes can target it.
      </p>
    </div>
  );
}

interface ProviderRowProps {
  provider: CustomProvider;
  status: RowStatus;
  onToggle: () => void;
  onEdit: () => void;
}

function ProviderRow({ provider, status, onToggle, onEdit }: ProviderRowProps) {
  return (
    <li className="flex items-center justify-between gap-4 px-4 py-3.5">
      <div className="flex min-w-0 flex-col gap-0.5">
        <span className="text-sm text-[var(--color-ink)]">
          {provider.display_name || provider.slug}
          {provider.display_name ? (
            <span className="ml-2 text-2xs text-[var(--color-ink-3)]">{provider.slug}</span>
          ) : null}
          {!provider.enabled ? (
            <span className="ml-2 rounded bg-[var(--color-surface-inset)] px-1.5 py-0.5 text-2xs uppercase tracking-wide text-[var(--color-ink-3)]">
              Disabled
            </span>
          ) : null}
        </span>
        <span className="truncate font-mono text-2xs text-[var(--color-ink-3)]">
          {provider.base_url || "no base URL"} · key: {provider.api_key_env}
          {provider.litellm_prefix ? ` · prefix: ${provider.litellm_prefix}` : ""}
        </span>
        {status === "error" ? (
          <span className="mt-0.5 flex items-center gap-1 text-2xs text-[var(--color-danger,#d64545)]">
            <AlertCircle size={12} />
            Could not save. Try again.
          </span>
        ) : null}
      </div>
      <div className="flex shrink-0 items-center gap-3">
        <button
          type="button"
          onClick={onEdit}
          disabled={status === "saving"}
          aria-label={`Edit ${provider.display_name || provider.slug}`}
          className="rounded border border-[var(--color-border)] px-2 py-1 text-2xs text-[var(--color-ink-2)] transition-colors hover:bg-[var(--color-surface-inset)] hover:text-[var(--color-ink)] cursor-pointer disabled:cursor-wait disabled:opacity-60"
        >
          Edit
        </button>
        <span
          className={cn(
            "text-2xs tabular-nums transition-opacity",
            status === "saving"
              ? "text-[var(--color-ink-3)] opacity-100"
              : "opacity-0",
          )}
          aria-hidden="true"
        >
          Saving…
        </span>
        <EnableSwitch
          checked={provider.enabled}
          saving={status === "saving"}
          label={provider.display_name || provider.slug}
          onToggle={onToggle}
        />
      </div>
    </li>
  );
}

interface EditFormProps {
  provider: CustomProvider;
  error: string | null;
  onCancel: () => void;
  onSave: (event: React.FormEvent<HTMLFormElement>) => void;
}

function EditForm({ provider, error, onCancel, onSave }: EditFormProps) {
  return (
    <form
      onSubmit={(event) => {
        event.preventDefault();
        onSave(event);
      }}
      className="flex flex-col gap-3"
    >
      <h3 className="text-2xs font-medium uppercase tracking-[0.14em] text-[var(--color-ink-3)]">
        Edit {provider.display_name || provider.slug}
      </h3>
      <div className="flex flex-wrap gap-3">
        <input
          name="slug"
          required
          defaultValue={provider.slug}
          className="min-w-[140px] flex-1 rounded border border-[var(--color-border)] bg-transparent px-2 py-1.5 text-sm"
        />
        <input
          name="display_name"
          defaultValue={provider.display_name}
          placeholder="Display name"
          className="min-w-[160px] flex-1 rounded border border-[var(--color-border)] bg-transparent px-2 py-1.5 text-sm"
        />
      </div>
      <input
        name="base_url"
        type="url"
        defaultValue={provider.base_url}
        placeholder="Base URL"
        className="rounded border border-[var(--color-border)] bg-transparent px-2 py-1.5 text-sm"
      />
      <div className="flex flex-wrap gap-3">
        <input
          name="api_key_env"
          required
          defaultValue={provider.api_key_env}
          className="min-w-[220px] flex-1 rounded border border-[var(--color-border)] bg-transparent px-2 py-1.5 font-mono text-sm"
        />
        <input
          name="litellm_prefix"
          defaultValue={provider.litellm_prefix}
          placeholder="LiteLLM prefix (optional)"
          className="min-w-[180px] flex-1 rounded border border-[var(--color-border)] bg-transparent px-2 py-1.5 font-mono text-sm"
        />
      </div>
      {error ? (
        <span className="flex items-center gap-1 text-2xs text-[var(--color-danger,#d64545)]">
          <AlertCircle size={12} />
          {error}
        </span>
      ) : null}
      <div className="flex gap-2">
        <button
          type="submit"
          className="cursor-pointer rounded bg-[var(--color-accent)] px-3 py-1.5 text-sm text-white"
        >
          Save changes
        </button>
        <button
          type="button"
          onClick={onCancel}
          className="cursor-pointer rounded border border-[var(--color-border)] px-3 py-1.5 text-sm text-[var(--color-ink-2)] hover:bg-[var(--color-surface-inset)]"
        >
          Cancel
        </button>
      </div>
    </form>
  );
}

interface EnableSwitchProps {
  checked: boolean;
  saving: boolean;
  label: string;
  onToggle: () => void;
}

function EnableSwitch({ checked, saving, label, onToggle }: EnableSwitchProps) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      aria-label={`${label}: ${checked ? "enabled" : "disabled"}`}
      disabled={saving}
      onClick={onToggle}
      className={cn(
        "relative inline-flex h-6 w-11 shrink-0 items-center rounded-full",
        "transition-colors duration-[var(--duration-fast)]",
        "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-accent)] focus-visible:ring-offset-2 focus-visible:ring-offset-[var(--color-surface)]",
        checked
          ? "bg-[var(--color-accent)]"
          : "bg-[var(--color-border-strong,#c9c9c9)]",
        saving ? "cursor-wait opacity-70" : "cursor-pointer",
      )}
    >
      <span
        className={cn(
          "inline-block h-5 w-5 transform rounded-full bg-white shadow-sm",
          "transition-transform duration-[var(--duration-fast)]",
          checked ? "translate-x-[22px]" : "translate-x-0.5",
        )}
      />
    </button>
  );
}

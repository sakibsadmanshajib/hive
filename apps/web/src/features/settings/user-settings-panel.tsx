"use client";

import { useEffect, useState } from "react";

import { Button } from "../../components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "../../components/ui/card";
import { apiBase as defaultApiBase, apiHeaders } from "../../lib/api";

type SettingKey = "apiEnabled" | "generateImage" | "chatEnabled" | "billingEnabled" | "twoFactorEnabled";

type UserSettings = Record<SettingKey, boolean>;

const defaultSettings: UserSettings = {
  apiEnabled: true,
  generateImage: true,
  chatEnabled: true,
  billingEnabled: true,
  twoFactorEnabled: false,
};

const settingMetadata: Array<{ key: SettingKey; label: string; description: string }> = [
  { key: "apiEnabled", label: "API enabled", description: "Allow API key usage and key management." },
  { key: "chatEnabled", label: "Chat enabled", description: "Allow chat completion requests." },
  { key: "generateImage", label: "Generate image", description: "Allow image generation requests." },
  { key: "billingEnabled", label: "Billing enabled", description: "Allow payment intent and billing actions." },
  { key: "twoFactorEnabled", label: "Two-factor enabled", description: "Mark account as enrolled for phase-1 2FA." },
];

type UserSettingsPanelProps = {
  accessToken: string;
  apiBase?: string;
};

function parseSettings(json: unknown): UserSettings {
  const settings = (json as { settings?: Partial<UserSettings> } | null)?.settings;
  return {
    apiEnabled: settings?.apiEnabled ?? defaultSettings.apiEnabled,
    generateImage: settings?.generateImage ?? defaultSettings.generateImage,
    chatEnabled: settings?.chatEnabled ?? defaultSettings.chatEnabled,
    billingEnabled: settings?.billingEnabled ?? defaultSettings.billingEnabled,
    twoFactorEnabled: settings?.twoFactorEnabled ?? defaultSettings.twoFactorEnabled,
  };
}

function endpointMessage(status: number, fallback: string): string {
  if (status === 404) {
    return "User settings endpoint is not available yet. Update backend routes and try again.";
  }

  return fallback;
}

export function UserSettingsPanel({ accessToken, apiBase = defaultApiBase }: UserSettingsPanelProps) {
  const [settings, setSettings] = useState<UserSettings>(defaultSettings);
  const [status, setStatus] = useState("Provide an API key to load feature settings.");
  const [loading, setLoading] = useState(false);
  const [updatingKey, setUpdatingKey] = useState<SettingKey | null>(null);

  useEffect(() => {
    async function loadSettings() {
      if (!accessToken) {
        setStatus("Provide an API key to load feature settings.");
        return;
      }

      setLoading(true);
      setStatus("Loading settings...");
      try {
        const response = await fetch(`${apiBase}/v1/users/settings`, {
          method: "GET",
          headers: apiHeaders(accessToken),
        });
        const json = await response.json().catch(() => ({}));

        if (!response.ok) {
          const fallback = typeof json.error === "string" ? json.error : "Failed to load user settings.";
          setStatus(endpointMessage(response.status, fallback));
          return;
        }

        setSettings(parseSettings(json));
        setStatus("Settings loaded.");
      } catch {
        setStatus("Could not reach settings endpoint. Check API availability and try again.");
      } finally {
        setLoading(false);
      }
    }

    void loadSettings();
  }, [apiBase, accessToken]);

  async function toggleSetting(key: SettingKey) {
    if (!accessToken || loading || updatingKey) {
      return;
    }

    const nextValue = !settings[key];
    const previousSettings = settings;

    setUpdatingKey(key);
    setSettings((current) => ({ ...current, [key]: nextValue }));
    setStatus(`Updating ${key}...`);

    try {
      const response = await fetch(`${apiBase}/v1/users/settings`, {
        method: "PATCH",
        headers: apiHeaders(accessToken),
        body: JSON.stringify({ [key]: nextValue }),
      });
      const json = await response.json().catch(() => ({}));

      if (!response.ok) {
        setSettings(previousSettings);
        const fallback = typeof json.error === "string" ? json.error : "Failed to update user settings.";
        setStatus(endpointMessage(response.status, fallback));
        return;
      }

      setSettings(parseSettings(json));
      setStatus("Settings updated.");
    } catch {
      setSettings(previousSettings);
      setStatus("Could not save settings. Check API availability and try again.");
    } finally {
      setUpdatingKey(null);
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>User feature settings</CardTitle>
        <CardDescription>Toggle account-level access gates. Changes apply immediately when endpoint support is available.</CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        {settingMetadata.map((setting) => {
          const checked = settings[setting.key];
          const isUpdating = updatingKey === setting.key;
          return (
            <div className="flex items-center justify-between gap-3 rounded-lg border p-3" key={setting.key}>
              <div>
                <p className="text-sm font-medium">{setting.label}</p>
                <p className="text-xs text-muted-foreground">{setting.description}</p>
              </div>
              <Button
                aria-checked={checked}
                aria-label={setting.label}
                disabled={loading || !!updatingKey}
                onClick={() => void toggleSetting(setting.key)}
                role="switch"
                size="sm"
                type="button"
                variant={checked ? "default" : "outline"}
              >
                {isUpdating ? "Saving..." : checked ? "On" : "Off"}
              </Button>
            </div>
          );
        })}
        <p aria-live="polite" className="text-sm text-muted-foreground">
          {status}
        </p>
      </CardContent>
    </Card>
  );
}

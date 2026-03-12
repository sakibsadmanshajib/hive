import type { SupabaseClient } from "@supabase/supabase-js";
import type { PersistentApiKey, PersistentApiKeyEvent } from "../domain/types";
import { hashApiKeyForLookup } from "./security";

type ApiKeyResolution = { userId: string; scopes: string[] };
type CreateApiKeyInput = {
  key: string;
  userId: string;
  scopes: string[];
  nickname: string;
  expiresAt?: string;
};

function extractApiKeyPrefix(rawKey: string): string {
  const parts = rawKey.split("_");
  if (parts.length >= 2) {
    return `${parts[0]}_${parts[1]}_`;
  }
  return rawKey.slice(0, 8);
}

export class SupabaseApiKeyStore {
  constructor(private readonly supabase: SupabaseClient) { }

  async create(input: CreateApiKeyInput): Promise<void> {
    const keyId = crypto.randomUUID();
    const { error } = await this.supabase.from("api_keys").insert({
      id: keyId,
      user_id: input.userId,
      key_hash: hashApiKeyForLookup(input.key),
      key_prefix: extractApiKeyPrefix(input.key),
      nickname: input.nickname,
      scopes: input.scopes,
      revoked: false,
      expires_at: input.expiresAt ?? null,
    });
    if (error) {
      throw new Error(`failed to create api key metadata: ${error.message}`);
    }
    try {
      await this.recordEvent({
        apiKeyId: keyId,
        userId: input.userId,
        eventType: "created",
        metadata: {
          keyPrefix: extractApiKeyPrefix(input.key),
          nickname: input.nickname,
        },
      });
    } catch (error) {
      await this.rollbackCreatedKey(keyId);
      throw error;
    }
  }

  async resolve(key: string): Promise<ApiKeyResolution | null> {
    const { data, error } = await this.supabase
      .from("api_keys")
      .select("id, user_id, scopes, revoked, expires_at")
      .eq("key_hash", hashApiKeyForLookup(key))
      .maybeSingle();
    if (error) {
      throw new Error(`failed to resolve api key metadata: ${error.message}`);
    }
    if (!data || data.revoked) {
      return null;
    }
    const expiresAt = this.toOptionalIsoString(data.expires_at);
    if (expiresAt && new Date(expiresAt).getTime() <= Date.now()) {
      const apiKeyId = String(data.id);
      const alreadyObserved = await this.hasEvent(apiKeyId, "expired_observed");
      if (!alreadyObserved) {
        await this.recordEvent({
          apiKeyId,
          userId: String(data.user_id),
          eventType: "expired_observed",
          metadata: { expiresAt },
        });
      }
      return null;
    }
    return {
      userId: String(data.user_id),
      scopes: Array.isArray(data.scopes) ? (data.scopes as string[]) : [],
    };
  }

  async list(userId: string): Promise<PersistentApiKey[]> {
    const { data, error } = await this.supabase
      .from("api_keys")
      .select("id, key_prefix, nickname, user_id, scopes, revoked, created_at, expires_at, revoked_at")
      .eq("user_id", userId);
    if (error) {
      throw new Error(`failed to list api key metadata: ${error.message}`);
    }
    return (data ?? []).map((row) => this.mapPersistentApiKey(row));
  }

  async revoke(key: string, userId: string): Promise<boolean> {
    const revokedAt = new Date().toISOString();
    const keyHash = hashApiKeyForLookup(key);
    const { data, error } = await this.supabase
      .from("api_keys")
      .update({ revoked: true, revoked_at: revokedAt })
      .eq("key_hash", keyHash)
      .eq("user_id", userId)
      .eq("revoked", false)
      .select();

    if (error) {
      throw new Error(`failed to revoke api key metadata: ${error.message}`);
    }

    if (!Array.isArray(data) || data.length === 0) {
      return false;
    }

    const row = data[0] as Record<string, unknown>;
    try {
      await this.recordEvent({
        apiKeyId: String(row.id),
        userId,
        eventType: "revoked",
        metadata: { revokedAt },
      });
    } catch (error) {
      await this.rollbackRevokedKey(String(row.id), userId);
      throw error;
    }
    return true;
  }

  async revokeById(id: string, userId: string): Promise<boolean> {
    const revokedAt = new Date().toISOString();
    const { data, error } = await this.supabase
      .from("api_keys")
      .update({ revoked: true, revoked_at: revokedAt })
      .eq("id", id)
      .eq("user_id", userId)
      .eq("revoked", false)
      .select();

    if (error) {
      throw new Error(`failed to revoke api key metadata: ${error.message}`);
    }

    if (!Array.isArray(data) || data.length === 0) {
      return false;
    }

    try {
      await this.recordEvent({
        apiKeyId: String((data[0] as Record<string, unknown>).id),
        userId,
        eventType: "revoked",
        metadata: { revokedAt },
      });
    } catch (error) {
      await this.rollbackRevokedKey(id, userId);
      throw error;
    }
    return true;
  }

  async get(key: string): Promise<PersistentApiKey | undefined> {
    const keyHash = hashApiKeyForLookup(key);
    const { data, error } = await this.supabase
      .from("api_keys")
      .select("id, key_prefix, nickname, user_id, scopes, revoked, created_at, expires_at, revoked_at")
      .eq("key_hash", keyHash)
      .maybeSingle();
    if (error) {
      throw new Error(`failed to get api key metadata: ${error.message}`);
    }
    if (!data) {
      return undefined;
    }
    return this.mapPersistentApiKey(data);
  }

  async listEvents(userId: string): Promise<PersistentApiKeyEvent[]> {
    const { data, error } = await this.supabase
      .from("api_key_events")
      .select("id, api_key_id, user_id, event_type, metadata, event_at")
      .eq("user_id", userId);
    if (error) {
      throw new Error(`failed to list api key events: ${error.message}`);
    }
    return (data ?? []).map((row) => ({
      id: String(row.id),
      apiKeyId: String(row.api_key_id),
      userId: String(row.user_id),
      eventType: String(row.event_type) as PersistentApiKeyEvent["eventType"],
      eventAt: new Date(row.event_at as string | Date).toISOString(),
      metadata: typeof row.metadata === "object" && row.metadata ? row.metadata as Record<string, unknown> : {},
    }));
  }

  private async recordEvent(input: {
    apiKeyId: string;
    userId: string;
    eventType: PersistentApiKeyEvent["eventType"];
    metadata?: Record<string, unknown>;
  }): Promise<void> {
    const { error } = await this.supabase.from("api_key_events").insert({
      api_key_id: input.apiKeyId,
      user_id: input.userId,
      event_type: input.eventType,
      metadata: input.metadata ?? {},
    });
    if (error) {
      throw new Error(`failed to record api key audit event: ${error.message}`);
    }
  }

  private async hasEvent(apiKeyId: string, eventType: PersistentApiKeyEvent["eventType"]): Promise<boolean> {
    const { data, error } = await this.supabase
      .from("api_key_events")
      .select("id")
      .eq("api_key_id", apiKeyId)
      .eq("event_type", eventType)
      .maybeSingle();
    if (error) {
      throw new Error(`failed to read api key audit event: ${error.message}`);
    }
    return Boolean(data);
  }

  private async rollbackCreatedKey(apiKeyId: string): Promise<void> {
    const { error } = await this.supabase
      .from("api_keys")
      .delete()
      .eq("id", apiKeyId);
    if (error) {
      throw new Error(`failed to rollback api key metadata create: ${error.message}`);
    }
  }

  private async rollbackRevokedKey(apiKeyId: string, userId: string): Promise<void> {
    const { error } = await this.supabase
      .from("api_keys")
      .update({ revoked: false, revoked_at: null })
      .eq("id", apiKeyId)
      .eq("user_id", userId)
      .select();
    if (error) {
      throw new Error(`failed to rollback api key revoke: ${error.message}`);
    }
  }

  private mapPersistentApiKey(row: Record<string, unknown>): PersistentApiKey {
    const expiresAt = this.toOptionalIsoString(row.expires_at);
    const revoked = Boolean(row.revoked);
    const status = revoked
      ? "revoked"
      : expiresAt && new Date(expiresAt).getTime() <= Date.now()
        ? "expired"
        : "active";
    return {
      id: String(row.id),
      keyPrefix: String(row.key_prefix),
      nickname: String(row.nickname),
      userId: String(row.user_id),
      scopes: Array.isArray(row.scopes) ? (row.scopes as string[]) : [],
      status,
      revoked,
      createdAt: new Date(row.created_at as string | Date).toISOString(),
      expiresAt,
      revokedAt: this.toOptionalIsoString(row.revoked_at),
    };
  }

  private toOptionalIsoString(value: unknown): string | undefined {
    if (!value) {
      return undefined;
    }
    return new Date(value as string | Date).toISOString();
  }
}

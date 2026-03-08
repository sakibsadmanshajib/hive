import type { SupabaseClient } from "@supabase/supabase-js";
import type { PersistentApiKey } from "../domain/types";
import { hashApiKeyForLookup } from "./security";

type ApiKeyResolution = { userId: string; scopes: string[] };

function extractApiKeyPrefix(rawKey: string): string {
  const parts = rawKey.split("_");
  if (parts.length >= 2) {
    return `${parts[0]}_${parts[1]}_`;
  }
  return rawKey.slice(0, 8);
}

export class SupabaseApiKeyStore {
  constructor(private readonly supabase: SupabaseClient) { }

  async create(input: { key: string; userId: string; scopes: string[] }): Promise<void> {
    const { error } = await this.supabase.from("api_keys").insert({
      user_id: input.userId,
      key_hash: hashApiKeyForLookup(input.key),
      key_prefix: extractApiKeyPrefix(input.key),
      scopes: input.scopes,
      revoked: false,
    });
    if (error) {
      throw new Error(`failed to create api key metadata: ${error.message}`);
    }
  }

  async resolve(key: string): Promise<ApiKeyResolution | null> {
    const { data, error } = await this.supabase
      .from("api_keys")
      .select("user_id, scopes, revoked")
      .eq("key_hash", hashApiKeyForLookup(key))
      .maybeSingle();
    if (error) {
      throw new Error(`failed to resolve api key metadata: ${error.message}`);
    }
    if (!data || data.revoked) {
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
      .select("key_prefix, user_id, scopes, revoked, created_at")
      .eq("user_id", userId);
    if (error) {
      throw new Error(`failed to list api key metadata: ${error.message}`);
    }
    return (data ?? []).map((row) => ({
      key: String(row.key_prefix),
      userId: String(row.user_id),
      scopes: Array.isArray(row.scopes) ? (row.scopes as string[]) : [],
      revoked: Boolean(row.revoked),
      createdAt: new Date(row.created_at as string | Date).toISOString(),
    }));
  }

  async revoke(key: string, userId: string): Promise<boolean> {
    const keyHash = hashApiKeyForLookup(key);
    const { data: record, error: lookupError } = await this.supabase
      .from("api_keys")
      .select("user_id, revoked")
      .eq("key_hash", keyHash)
      .maybeSingle();
    if (lookupError) {
      throw new Error(`failed to query api key metadata for revoke: ${lookupError.message}`);
    }
    if (!record || record.revoked || String(record.user_id) !== userId) {
      return false;
    }

    const { error } = await this.supabase.from("api_keys").update({ revoked: true, revoked_at: new Date().toISOString() }).eq("key_hash", keyHash).eq("user_id", userId);
    if (error) {
      throw new Error(`failed to revoke api key metadata: ${error.message}`);
    }
    return true;
  }

  async get(key: string): Promise<PersistentApiKey | undefined> {
    const keyHash = hashApiKeyForLookup(key);
    const { data, error } = await this.supabase
      .from("api_keys")
      .select("key_prefix, user_id, scopes, revoked, created_at")
      .eq("key_hash", keyHash)
      .maybeSingle();
    if (error) {
      throw new Error(`failed to get api key metadata: ${error.message}`);
    }
    if (!data) {
      return undefined;
    }
    return {
      key: String(data.key_prefix),
      userId: String(data.user_id),
      scopes: Array.isArray(data.scopes) ? (data.scopes as string[]) : [],
      revoked: Boolean(data.revoked),
      createdAt: new Date(data.created_at as string | Date).toISOString(),
    };
  }
}

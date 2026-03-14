import { randomUUID } from "node:crypto";
import type { SupabaseClient } from "@supabase/supabase-js";

export type GuestUsageEvent = {
  id: string;
  guestId: string;
  endpoint: string;
  model: string;
  credits: number;
  ipAddress?: string;
  createdAt: string;
};

export class SupabaseGuestAttributionStore {
  constructor(private readonly supabase: SupabaseClient) {}

  async upsertSession(input: {
    guestId: string;
    expiresAt: string;
    lastSeenIp?: string;
  }): Promise<void> {
    const { error } = await this.supabase.from("guest_sessions").upsert(
      {
        guest_id: input.guestId,
        expires_at: input.expiresAt,
        last_seen_ip: input.lastSeenIp ?? null,
        last_seen_at: new Date().toISOString(),
      },
      { onConflict: "guest_id" },
    );

    if (error) {
      throw new Error(`failed to persist guest session: ${error.message}`);
    }
  }

  async addUsage(input: {
    guestId: string;
    endpoint: string;
    model: string;
    credits: number;
    ipAddress?: string;
  }): Promise<GuestUsageEvent> {
    const id = `guest_usage_${randomUUID()}`;
    const { data, error } = await this.supabase.from("guest_usage_events").insert({
      id,
      guest_id: input.guestId,
      endpoint: input.endpoint,
      model: input.model,
      credits: input.credits,
      ip_address: input.ipAddress ?? null,
    }).select("created_at").single();

    if (error) {
      throw new Error(`failed to persist guest usage: ${error.message}`);
    }

    return {
      id,
      guestId: input.guestId,
      endpoint: input.endpoint,
      model: input.model,
      credits: input.credits,
      ipAddress: input.ipAddress,
      createdAt: new Date(data.created_at as string | Date).toISOString(),
    };
  }

  async linkGuestToUser(input: {
    guestId: string;
    userId: string;
    linkSource: string;
  }): Promise<void> {
    const { error } = await this.supabase.from("guest_user_links").upsert(
      {
        guest_id: input.guestId,
        user_id: input.userId,
        link_source: input.linkSource,
      },
      { onConflict: "guest_id,user_id" },
    );

    if (error) {
      throw new Error(`failed to persist guest link: ${error.message}`);
    }
  }
}

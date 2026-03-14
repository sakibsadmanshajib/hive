import { describe, expect, it, vi } from "vitest";

import { SupabaseGuestAttributionStore } from "../../src/runtime/supabase-guest-attribution-store";

describe("SupabaseGuestAttributionStore", () => {
  it("upserts guest sessions into dedicated guest session storage", async () => {
    const upsert = vi.fn(async () => ({ error: null }));
    const supabase = {
      from: vi.fn((table: string) => {
        if (table !== "guest_sessions") {
          throw new Error(`unexpected table ${table}`);
        }
        return { upsert };
      }),
    };

    const store = new SupabaseGuestAttributionStore(supabase as never);

    await store.upsertSession({
      guestId: "guest_123",
      expiresAt: "2026-03-20T00:00:00.000Z",
      lastSeenIp: "203.0.113.10",
    });

    expect(upsert).toHaveBeenCalledWith(
      expect.objectContaining({
        guest_id: "guest_123",
        expires_at: "2026-03-20T00:00:00.000Z",
        last_seen_ip: "203.0.113.10",
      }),
      { onConflict: "guest_id" },
    );
  });

  it("records guest usage events separately from authenticated usage", async () => {
    const insert = vi.fn().mockReturnValue({
      select: vi.fn().mockReturnValue({
        single: vi.fn(async () => ({
          data: { created_at: "2026-03-13T12:00:00.000Z" },
          error: null,
        })),
      }),
    });
    const supabase = {
      from: vi.fn((table: string) => {
        if (table !== "guest_usage_events") {
          throw new Error(`unexpected table ${table}`);
        }
        return { insert };
      }),
    };

    const store = new SupabaseGuestAttributionStore(supabase as never);
    const event = await store.addUsage({
      guestId: "guest_123",
      endpoint: "/v1/web/chat/guest",
      model: "guest-free",
      credits: 0,
      ipAddress: "203.0.113.10",
    });

    expect(event).toEqual(
      expect.objectContaining({
        guestId: "guest_123",
        endpoint: "/v1/web/chat/guest",
        model: "guest-free",
        credits: 0,
        ipAddress: "203.0.113.10",
        createdAt: "2026-03-13T12:00:00.000Z",
      }),
    );
    expect(insert).toHaveBeenCalledWith(
      expect.objectContaining({
        guest_id: "guest_123",
        endpoint: "/v1/web/chat/guest",
        model: "guest-free",
        credits: 0,
        ip_address: "203.0.113.10",
      }),
    );
  });

  it("persists guest to user attribution links", async () => {
    const upsert = vi.fn(async () => ({ error: null }));
    const supabase = {
      from: vi.fn((table: string) => {
        if (table !== "guest_user_links") {
          throw new Error(`unexpected table ${table}`);
        }
        return { upsert };
      }),
    };

    const store = new SupabaseGuestAttributionStore(supabase as never);

    await store.linkGuestToUser({
      guestId: "guest_123",
      userId: "dd7964a8-5242-4434-a793-e9c6cb9dc4d5",
      linkSource: "auth_session",
    });

    expect(upsert).toHaveBeenCalledWith(
      expect.objectContaining({
        guest_id: "guest_123",
        user_id: "dd7964a8-5242-4434-a793-e9c6cb9dc4d5",
        link_source: "auth_session",
      }),
      { onConflict: "guest_id,user_id" },
    );
  });
});

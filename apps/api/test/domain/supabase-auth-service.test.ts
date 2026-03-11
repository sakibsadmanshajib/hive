import { describe, expect, it, vi } from "vitest";
import { SupabaseAuthService } from "../../src/runtime/supabase-auth-service";

describe("SupabaseAuthService", () => {
    it("returns session principal from valid token", async () => {
        const supabase = {
            auth: {
                getUser: vi.fn(async () => ({
                    data: { user: { id: "user_abc" } },
                    error: null,
                })),
            },
        };

        const service = new SupabaseAuthService(supabase as never);
        const principal = await service.getSessionPrincipal("valid_token");

        expect(principal).toEqual({ userId: "user_abc" });
        expect(supabase.auth.getUser).toHaveBeenCalledWith("valid_token");
    });

    it("returns null for invalid token", async () => {
        const supabase = {
            auth: {
                getUser: vi.fn(async () => ({
                    data: { user: null },
                    error: { message: "invalid token" },
                })),
            },
        };

        const service = new SupabaseAuthService(supabase as never);
        const principal = await service.getSessionPrincipal("bad_token");

        expect(principal).toBeNull();
    });

    it("returns null when user id is missing", async () => {
        const supabase = {
            auth: {
                getUser: vi.fn(async () => ({
                    data: { user: { id: undefined } },
                    error: null,
                })),
            },
        };

        const service = new SupabaseAuthService(supabase as never);
        const principal = await service.getSessionPrincipal("token_no_id");

        expect(principal).toBeNull();
    });
});

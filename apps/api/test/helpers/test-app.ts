import Fastify from "fastify";
import { TypeBoxTypeProvider } from "@fastify/type-provider-typebox";
import { registerRoutes } from "../../src/routes";
import type { RuntimeServices } from "../../src/runtime/services";

export type MockServices = {
  users: {
    resolveApiKey: (key: string) => Promise<{ userId: string; scopes: string[]; apiKeyId?: string } | null>;
  };
  env: {
    allowDevApiKeyPrefix: boolean;
    supabase: { flags: { authEnabled: boolean } };
  };
  supabaseAuth: {
    getSessionPrincipal: () => Promise<null>;
  };
  authz: {
    requirePermission: () => Promise<boolean>;
  };
  userSettings: {
    getForUser: () => Promise<{ apiEnabled: boolean }>;
    canUse: () => boolean;
  };
};

export function createMockServices(validApiKey: string, userId: string): MockServices {
  return {
    users: {
      resolveApiKey: async (key: string) => {
        if (key === validApiKey) {
          return { userId, scopes: ["chat", "image", "usage", "billing"], apiKeyId: "key_test" };
        }
        return null;
      },
    },
    env: {
      allowDevApiKeyPrefix: false,
      supabase: { flags: { authEnabled: false } },
    },
    supabaseAuth: {
      getSessionPrincipal: async () => null,
    },
    authz: {
      requirePermission: async () => true,
    },
    userSettings: {
      getForUser: async () => ({ apiEnabled: true }),
      canUse: () => true,
    },
  };
}

export async function createTestApp(mockServices: MockServices) {
  const app = Fastify({
    logger: false,
    ajv: {
      customOptions: {
        removeAdditional: false,
      },
    },
  }).withTypeProvider<TypeBoxTypeProvider>();

  registerRoutes(app, mockServices as unknown as RuntimeServices);

  const address = await app.listen({ port: 0 });
  return { app, address };
}

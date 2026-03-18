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
  models: {
    list: () => Array<{ id: string; object: string; created: number; capability: string; costType: string }>;
    findById: (modelId: string) => { id: string; object: string; created: number; capability: string; costType: string } | undefined;
  };
  rateLimiter: {
    allow: () => Promise<boolean>;
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
    models: {
      list: () => [
        { id: "mock-chat", object: "model", created: 1700000000, capability: "chat", costType: "paid" },
      ],
      findById: (modelId: string) => {
        const models = [
          { id: "mock-chat", object: "model", created: 1700000000, capability: "chat", costType: "paid" },
        ];
        return models.find((m) => m.id === modelId);
      },
    },
    rateLimiter: {
      allow: async () => true,
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

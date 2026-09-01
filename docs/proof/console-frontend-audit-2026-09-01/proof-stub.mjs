import { createServer } from "node:http";

// Proof harness stub for PR console-frontend-audit. Serves the two upstreams
// the console talks to (Supabase Auth and the control plane) with fixed
// fixtures, so the screenshots exercise the real built console against known
// rows rather than live tenant data.

const PORT = 4599;
const RLO = "\u202E";

const USER = {
  id: "11111111-1111-1111-1111-111111111111",
  aud: "authenticated",
  role: "authenticated",
  email: "proof@example.com",
  email_confirmed_at: "2026-01-01T00:00:00Z",
  phone: "",
  confirmed_at: "2026-01-01T00:00:00Z",
  app_metadata: { provider: "email", providers: ["email"] },
  user_metadata: {},
  identities: [],
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-01T00:00:00Z",
};

function jwt(payload) {
  const enc = (obj) =>
    Buffer.from(JSON.stringify(obj)).toString("base64url");
  return `${enc({ alg: "HS256", typ: "JWT" })}.${enc(payload)}.proof-signature`;
}

export const ACCESS_TOKEN = jwt({
  sub: USER.id,
  email: USER.email,
  role: "authenticated",
  tenant_id: "22222222-2222-2222-2222-222222222222",
  aud: "authenticated",
  iat: Math.floor(Date.now() / 1000),
  exp: Math.floor(Date.now() / 1000) + 60 * 60 * 24,
});

const SESSION = {
  access_token: ACCESS_TOKEN,
  token_type: "bearer",
  expires_in: 86400,
  expires_at: Math.floor(Date.now() / 1000) + 86400,
  refresh_token: "proof-refresh-token",
  user: USER,
};

const VIEWER = {
  user: { id: USER.id, email: USER.email, email_verified: true },
  current_account: {
    id: "22222222-2222-2222-2222-222222222222",
    display_name: "Proof Workspace",
    account_type: "individual",
    role: "owner",
  },
  memberships: [
    {
      account_id: "22222222-2222-2222-2222-222222222222",
      display_name: "Proof Workspace",
      role: "owner",
      status: "active",
    },
  ],
  permissions: ["analytics.view", "api_keys.write"],
};

function priced(input, output) {
  return {
    input_price_credits: input,
    output_price_credits: output,
    cache_read_price_credits: 0,
    cache_write_price_credits: 0,
    pricing_mode: "fixed",
  };
}

// The badge arrays are the ones the migrations seed for these aliases.
const MODELS = [
  {
    id: "hive-small",
    display_name: "Hive Small",
    summary: "Fast, low-cost chat for everyday prompts.",
    capability_badges: ["stable", "chat", "responses"],
    pricing: priced(10500, 42000),
    lifecycle: "stable",
  },
  {
    id: "hive-fast",
    display_name: "Hive Fast",
    summary: "Deprecated alias, still resolvable at the same price.",
    capability_badges: ["stable", "chat", "responses"],
    pricing: priced(10500, 42000),
    lifecycle: "hidden",
  },
  {
    id: "hive-embedding-default",
    display_name: "Hive Embedding Default",
    summary: "Default multimodal embedding alias.",
    capability_badges: ["stable", "embeddings"],
    pricing: priced(1, 0),
    lifecycle: "stable",
  },
  {
    id: "hive-stt",
    display_name: "Hive Voice STT",
    summary: "Serverless speech-to-text for /v1/audio/transcriptions.",
    capability_badges: ["voice", "stt"],
    pricing: priced(0, 500),
    lifecycle: "stable",
  },
  {
    id: "hive-tts",
    display_name: "Hive Voice TTS",
    summary: "Serverless text-to-speech for /v1/audio/speech.",
    capability_badges: ["voice", "tts"],
    pricing: priced(0, 1000),
    lifecycle: "stable",
  },
];

// One stored nickname carrying U+202E, the row shape issue #1653 found live.
const API_KEYS = [
  {
    id: "key-1",
    nickname: `prod${RLO}gnp.txt`,
    status: "active",
    redacted_suffix: "9f2a",
    created_at: "2026-08-01T10:00:00Z",
    updated_at: "2026-08-01T10:00:00Z",
  },
];

const ROUTES = new Map([
  ["GET /auth/v1/user", () => [200, USER]],
  ["POST /auth/v1/token", () => [200, SESSION]],
  ["GET /api/v1/viewer", () => [200, VIEWER]],
  ["GET /api/v1/catalog/models", () => [200, { models: MODELS }]],
  ["GET /api/v1/accounts/current/api-keys", () => [200, { items: API_KEYS }]],
  [
    "GET /api/v1/accounts/current/usage-events",
    () => [200, { events: [], next_cursor: "" }],
  ],
  [
    "GET /api/v1/accounts/current/profile",
    () => [200, { owner_name: "Proof Owner" }],
  ],
  [
    "GET /api/v1/accounts/current/credits/balance",
    () => [
      200,
      { posted_credits: 250000, reserved_credits: 0, available_credits: 250000 },
    ],
  ],
]);

const server = createServer((req, res) => {
  const url = new URL(req.url ?? "/", `http://127.0.0.1:${PORT}`);
  const key = `${req.method} ${url.pathname}`;
  const handler = ROUTES.get(key);
  const [status, body] = handler
    ? handler()
    : [404, { error: "not found on the proof stub: " + key }];
  const payload = JSON.stringify(body);
  console.log(`${key} -> ${status}`);
  res.writeHead(status, {
    "content-type": "application/json",
    "content-length": Buffer.byteLength(payload),
  });
  res.end(payload);
});

server.listen(PORT, "127.0.0.1", () => {
  console.log(`proof stub listening on http://127.0.0.1:${PORT}`);
});

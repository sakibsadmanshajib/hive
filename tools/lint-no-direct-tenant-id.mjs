// tools/lint-no-direct-tenant-id.mjs
// Block Go handlers that take tenant_id from the request body, query string,
// or header. tenant_id must always come from the resolved auth context via
// auth.TenantID(ctx) so RLS, RBAC, and audit cannot be spoofed.

import { readFileSync } from 'node:fs';
import { execSync } from 'node:child_process';

const ALLOWLIST_DIRS = [
  'apps/control-plane/internal/tenants/',         // tenant-switch handler reads from body deliberately
  'apps/control-plane/internal/signup/',          // webhook receives user_id (not tenant_id) from Supabase
  'apps/control-plane/internal/tenant/settings/', // LISTEN/NOTIFY payload, not HTTP request
  'apps/edge-api/internal/auth/',                 // ctx writers
  'apps/control-plane/internal/auditarchive/',    // system cron job; iterates ALL tenants cross-tenant — no request auth context, tenant_id is internal archive struct field not wire input
  'apps/control-plane/internal/egress/',          // tenant_id in these structs is RESPONSE wire shape (egress-policy read/CRUD JSON output); the handler always resolves tenant_id by parsing the URL path segment via uuid.Parse, never FormValue/Query/Header/request-body — no violation of the auth.TenantID(ctx) input rule this lint enforces
  'apps/agent-engine/internal/egressclient/',     // client-side counterpart decoding that same egress-policy RESPONSE wire shape; tenantID is an explicit caller-supplied parameter to a client library, not read off an inbound request
  'apps/control-plane/internal/marketplace/',     // tenant_id in these structs is RESPONSE wire shape (internal mcp-servers read + its own test), same posture as apps/control-plane/internal/egress/: the handler always resolves tenant_id by parsing the URL path segment via uuid.Parse, never FormValue/Query/Header/request-body — no violation of the auth.TenantID(ctx) input rule this lint enforces
  'apps/agent-engine/cmd/agent-engine/serve.go',  // internal launch daemon (issue #780), reachable only over a 0600 Unix socket in a 0700 directory and gated by the shared internal token; tenant_id crosses the process boundary in the body because the authenticated user's request context cannot cross processes -- control-plane (the only caller) fills it solely from the task row it created under auth.TenantID(ctx), see apps/control-plane/internal/agentengine/remote.go; same trust shape as the internal RAG ingest and routing endpoints below
  'apps/agent-engine/internal/marketplaceclient/', // client-side counterpart decoding that same marketplace RESPONSE wire shape; tenantID is an explicit caller-supplied parameter to a client library, not read off an inbound request
  'apps/control-plane/internal/rag/http.go',      // internal service-to-service /internal/rag/ingest endpoint, gated by RequireInternalToken shared secret (not a customer-facing route); tenant_id crosses the process boundary in the body because the authenticated user's request context cannot cross processes -- edge-api (the only caller) populates it solely from auth.TenantID(ctx), see TestHandleUpload_IngestUsesAuthenticatedTenantOnly; same trust shape as filestore's account_id-in-body pattern
  'apps/edge-api/internal/rag/ingestclient.go',   // client-side counterpart of the above: marshals the tenant_id already resolved from auth.TenantID(ctx) by the caller into the internal ingest request body -- not a wire-input path, no client ever supplies this value (UploadRequest has no tenant_id field)
  'apps/control-plane/internal/routing/http.go',  // internal service-to-service /internal/routing/select endpoint, gated by RequireInternalToken shared secret (not a customer-facing route); tenant_id crosses the process boundary in the body because the authenticated user's request context cannot cross processes -- edge-api (the only caller) overwrites it from auth.TenantID(ctx) on every call, see TestSelectRouteIgnoresCallerSuppliedTenant; same trust shape as the internal RAG ingest endpoint above
  'apps/edge-api/internal/inference/routing_client.go', // client-side counterpart of the above: RoutingClient.SelectRoute discards any caller-set value and sets tenant_id solely from auth.TenantID(ctx), so no client-supplied value can reach the wire
  'apps/control-plane/internal/apikeys/types.go', // tenant_id in apikeys.AuthSnapshot is RESPONSE wire shape from the internal /internal/apikeys/resolve endpoint, gated by cpauth shared secret (not a customer-facing route); Service.ResolveSnapshot resolves it server-side via GetTenantIDByAccountID against public.tenant_billing_accounts, never from client input (D-030 tenant-on-key)
  'apps/edge-api/internal/authz/authz.go', // client-side counterpart of the above: edge-api's AuthSnapshot.TenantID only ever comes from decoding that same control-plane RESPONSE body (authz.Client.Resolve / authz.Resolver.Resolve), never set from request input
  'apps/control-plane/internal/auth/client.go', // control-plane's own ctx WRITER, the exact counterpart of apps/edge-api/internal/auth/ above and structurally unable to obey this rule: it is what produces the value auth.TenantID(ctx) later returns, so consuming that helper here would be circular. tenant_id in auth.tenantClaims is not wire input from the caller's request either: it is a claim minted by public.custom_access_token_hook, a SECURITY DEFINER function that resolves it from one snapshot of ACTIVE public.tenant_users joined to non-archived public.tenants and omits the claim entirely for a user with no membership. It is read out of the bearer ONLY after Supabase has accepted that same bearer through GET /auth/v1/user (that round trip is the signature check, so the payload is GoTrue-authored, not attacker-authored), only when the token's sub matches the user id Supabase resolved from it, and the resulting tenant still passes the same live membershipCheck the self-asserted user_metadata path does, failing closed to uuid.Nil on refusal or error. Scoped to this one file rather than the package so a future handler added under internal/auth/ is still linted
  'supabase/migrations/',
  'tools/lint-no-direct-tenant-id.mjs',
];

const FORBIDDEN = [
  /\.FormValue\(\s*"tenant_id"\s*\)/,
  /\.Get\(\s*"X-Tenant-Id"\s*\)/i,
  /r\.URL\.Query\(\)\.Get\(\s*"tenant_id"\s*\)/,
  /json:"tenant_id"/,    // a Go struct receiving tenant_id from the wire is a smell
];

const DIR_RE = /^(apps|packages)\//;
const EXT_RE = /\.go$/;

const files = execSync('git ls-files', { encoding: 'utf8' })
  .split('\n')
  .filter(Boolean)
  .filter(f => DIR_RE.test(f) && EXT_RE.test(f));

let violations = 0;
for (const file of files) {
  if (ALLOWLIST_DIRS.some(p => file.startsWith(p))) continue;
  const text = readFileSync(file, 'utf8');
  for (const re of FORBIDDEN) {
    if (re.test(text)) {
      const lines = text.split('\n');
      lines.forEach((line, i) => {
        if (re.test(line)) {
          console.error(`${file}:${i + 1}: forbidden direct tenant_id read — use auth.TenantID(ctx)`);
          violations++;
        }
      });
    }
  }
}

if (violations > 0) {
  console.error(`\n${violations} tenant-id lint violation(s).`);
  process.exit(1);
}
console.log('lint-no-direct-tenant-id: PASS');

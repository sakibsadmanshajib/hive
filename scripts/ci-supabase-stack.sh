#!/usr/bin/env bash
# ci-supabase-stack.sh -- stand up a throwaway Supabase HTTP surface (GoTrue +
# PostgREST behind one origin) on top of a throwaway Postgres.
#
# Why this exists as well as scripts/ci-throwaway-db.sh
# ----------------------------------------------------
# Some CI jobs need more than a database. The Playwright console suite signs a
# browser in through @supabase/ssr against NEXT_PUBLIC_SUPABASE_URL, and its
# fixture seeder (apps/web-console/tests/e2e/support/e2e-fixture-seed.mjs) uses
# supabase-js: auth.admin.createUser against /auth/v1 and .from(table) against
# /rest/v1. A bare Postgres serves neither, so those jobs could not be moved off
# the shared hosted project by pointing a DSN somewhere else.
#
# The coupling is not separable either. GoTrue's custom access token hook
# (supabase/migrations/20260516_07) runs INSIDE the database holding the user
# rows and raises no_active_membership for a user with no tenant_users row, so
# a job with cloud auth and a local database fails every single login. It is
# all or nothing per job, which is why this brings up the whole surface.
#
# Shape
#   supabase-db        the throwaway Postgres (already running, named by
#                      --db-container, so this shares one database with
#                      scripts/ci-throwaway-db.sh rather than starting a second)
#   supabase-auth      GoTrue, which owns and migrates the auth schema
#   supabase-rest      PostgREST, started AFTER the migration chain so its
#                      schema cache holds the real tables
#   supabase-gw        nginx, mapping /auth/v1/ and /rest/v1/ onto those two,
#                      because supabase-js takes ONE base URL and appends those
#                      prefixes itself
#
# Ordering matters and is not incidental:
#   1. create the auth schema and roles      (00-extensions.sql)
#   2. GoTrue migrates auth.*                (13 migrations foreign-key to
#                                             auth.users, so it has to be first)
#   3. scripts/ci-throwaway-db.sh --gotrue   (the Hive chain, which also creates
#                                             public.custom_access_token_hook)
#   4. PostgREST                             (schema cache after the chain)
#
# Everything here is thrown away with the runner. The JWT secret and the two
# API keys are generated per run, are worth nothing outside this job, and are
# masked by the caller.

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

db_container="hive-ci-db"
db_name="hive_ci"
db_user="postgres"
db_password="postgres"
network="hive-ci-supabase"
gateway_port="9000"

while [ "$#" -gt 0 ]; do
  case "$1" in
    --db-container) db_container="$2"; shift 2 ;;
    --db-name) db_name="$2"; shift 2 ;;
    --port) gateway_port="$2"; shift 2 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done

log() { echo "$@" >&2; }

# ---------------------------------------------------------------------------
# Keys. HS256, which is what self-hosted GoTrue and PostgREST both speak when
# given a shared secret. No expiry games: these live for one job.
# ---------------------------------------------------------------------------
jwt_secret="$(openssl rand -hex 32)"
mint_key() {
  python3 - "$jwt_secret" "$1" <<'PY'
import base64, hashlib, hmac, json, sys, time
secret, role = sys.argv[1], sys.argv[2]
def b64(raw): return base64.urlsafe_b64encode(raw).rstrip(b"=").decode()
header = b64(json.dumps({"alg": "HS256", "typ": "JWT"}, separators=(",", ":")).encode())
now = int(time.time())
payload = b64(json.dumps(
    {"role": role, "iss": "supabase", "iat": now, "exp": now + 24 * 3600},
    separators=(",", ":"),
).encode())
signing_input = f"{header}.{payload}".encode()
sig = b64(hmac.new(secret.encode(), signing_input, hashlib.sha256).digest())
print(f"{header}.{payload}.{sig}")
PY
}
anon_key="$(mint_key anon)"
service_role_key="$(mint_key service_role)"

# ---------------------------------------------------------------------------
# Network. The database container is already running and published on the host;
# it is attached to a user-defined network here so GoTrue and PostgREST can
# resolve it by name.
# ---------------------------------------------------------------------------
docker network inspect "$network" >/dev/null 2>&1 || docker network create "$network" >/dev/null
# docker network connect exits non-zero both when the container is already
# attached and when it does not exist, and `|| true` cannot tell those apart.
# Without this check a caller that renamed or failed to start the database
# container gets "GoTrue never became healthy" forty lines later, which names
# the wrong component.
if ! docker inspect -f '{{.State.Running}}' "$db_container" 2>/dev/null | grep -qx true; then
  log "::error::the database container '$db_container' is not running; start it before this script"
  exit 1
fi
docker network connect --alias supabase-db "$network" "$db_container" 2>/dev/null || true

db_url="postgres://${db_user}:${db_password}@supabase-db:5432/${db_name}"

log "==> auth schema and roles, before GoTrue migrates into them"
psql --no-psqlrc -qX -v ON_ERROR_STOP=1 -f "$repo_root/deploy/supabase/init/00-extensions.sql" >/dev/null

log "==> GoTrue"
docker rm -f supabase-auth >/dev/null 2>&1 || true
docker run -d --name supabase-auth --network "$network" \
  -e GOTRUE_API_HOST=0.0.0.0 \
  -e GOTRUE_API_PORT=9999 \
  -e "API_EXTERNAL_URL=http://localhost:${gateway_port}" \
  -e GOTRUE_DB_DRIVER=postgres \
  -e "GOTRUE_DB_DATABASE_URL=${db_url}?search_path=auth&sslmode=disable" \
  -e "GOTRUE_SITE_URL=http://localhost:3000" \
  -e "GOTRUE_URI_ALLOW_LIST=http://localhost:3000/**,http://127.0.0.1:3000/**" \
  -e "GOTRUE_JWT_SECRET=${jwt_secret}" \
  -e GOTRUE_JWT_AUD=authenticated \
  -e GOTRUE_JWT_ADMIN_ROLES=service_role \
  -e GOTRUE_JWT_EXP=3600 \
  -e GOTRUE_DISABLE_SIGNUP=false \
  -e GOTRUE_EXTERNAL_EMAIL_ENABLED=true \
  -e GOTRUE_MAILER_AUTOCONFIRM=false \
  -e GOTRUE_SMTP_HOST= \
  -e GOTRUE_PASSWORD_MIN_LENGTH=6 \
  -e GOTRUE_HOOK_CUSTOM_ACCESS_TOKEN_ENABLED=true \
  -e "GOTRUE_HOOK_CUSTOM_ACCESS_TOKEN_URI=pg-functions://postgres/public/custom_access_token_hook" \
  supabase/gotrue:v2.170.0 >/dev/null

for _ in $(seq 1 60); do
  if docker exec supabase-auth wget -q -O /dev/null http://localhost:9999/health 2>/dev/null; then break; fi
  sleep 2
done
if ! docker exec supabase-auth wget -q -O /dev/null http://localhost:9999/health 2>/dev/null; then
  log "::error::GoTrue never became healthy"
  docker logs supabase-auth 2>&1 | tail -30 >&2
  exit 1
fi
if [ "$(psql --no-psqlrc -qtAX -c "SELECT to_regclass('auth.users') IS NOT NULL")" != "t" ]; then
  log "::error::GoTrue started but auth.users does not exist, so the Hive chain would fail on its foreign keys"
  exit 1
fi
log "GoTrue is up and owns the auth schema"

log "==> Hive migration chain"
"$repo_root/scripts/ci-throwaway-db.sh" --gotrue >&2

log "==> API-role grants the hosted platform applies for us"
# On a hosted Supabase project the platform holds default privileges that give
# anon, authenticated and service_role table access in public; RLS is what
# actually gates anon and authenticated, and service_role is BYPASSRLS (see
# deploy/supabase/init/00-extensions.sql). supabase/migrations grants none of
# this because it never had to. Without it PostgREST answers 403 to every
# request, including the service-role fixture seeding. Applied after the chain
# so it covers every table the chain created.
psql --no-psqlrc -qX -v ON_ERROR_STOP=1 >/dev/null <<'SQL'
GRANT ALL ON ALL TABLES    IN SCHEMA public TO anon, authenticated, service_role;
GRANT ALL ON ALL SEQUENCES IN SCHEMA public TO anon, authenticated, service_role;
GRANT ALL ON ALL FUNCTIONS IN SCHEMA public TO anon, authenticated, service_role;
ALTER DEFAULT PRIVILEGES IN SCHEMA public
  GRANT ALL ON TABLES TO anon, authenticated, service_role;
SQL

log "==> PostgREST"
docker rm -f supabase-rest >/dev/null 2>&1 || true
docker run -d --name supabase-rest --network "$network" \
  -e "PGRST_DB_URI=${db_url}" \
  -e "PGRST_DB_SCHEMAS=public,storage,graphql_public" \
  -e PGRST_DB_ANON_ROLE=anon \
  -e "PGRST_JWT_SECRET=${jwt_secret}" \
  -e PGRST_DB_USE_LEGACY_GUCS=false \
  postgrest/postgrest:v12.2.3 >/dev/null

log "==> gateway"
# The config travels as an environment variable and is written by the
# container's own shell, rather than bind mounted. A bind mount of a file
# created by whatever process runs this script is not portable: under Docker
# Desktop the path does not exist on the daemon's host at all, and the run dies
# with "not a directory".
read -r -d '' gw_conf <<'NGINX' || true
server {
  listen 80;
  # supabase-js takes ONE base URL and appends /auth/v1 and /rest/v1 itself,
  # so a job cannot point it at GoTrue and PostgREST separately.
  location /auth/v1/ {
    # This gateway answers the CORS preflight itself instead of proxying it,
    # because GoTrue refuses the one supabase-js actually sends.
    #
    # GoTrue's CORS allow-list is a fixed set (Accept, Authorization,
    # Content-Type, X-Client-Info, X-Supabase-Api-Version and a few more) and
    # `apikey` is NOT in it. supabase-js puts `apikey` on every request, so the
    # browser preflight asks for a header GoTrue will not allow, and GoTrue
    # answers 204 with no Access-Control-* headers whatsoever. The browser then
    # blocks the request before it is sent: signInWithPassword rejects with
    # "Failed to fetch", which apps/web-console/lib/auth/auth-error.ts (an
    # allow-list by design) degrades to generic "Something went wrong on our
    # end" copy. Every credentialed spec then dies in its own signIn() helper on
    # a navigation timeout, 25 seconds at a time, naming nothing.
    #
    # On a hosted Supabase project this never surfaces because Kong terminates
    # CORS at the edge and never asks GoTrue about it. This is that same
    # termination, and it is the reason the gateway exists rather than an extra.
    #
    # PostgREST deliberately gets no such block: it already echoes the requested
    # headers back with an Access-Control-Allow-Origin, so adding one here would
    # emit the header twice and browsers reject a duplicated value.
    if ($request_method = OPTIONS) {
      add_header Access-Control-Allow-Origin  "*" always;
      add_header Access-Control-Allow-Methods "GET, POST, PUT, PATCH, DELETE, OPTIONS" always;
      # Echoed rather than hardcoded, the way PostgREST answers the same
      # preflight. A fixed list is what broke this in the first place, and a
      # future supabase-js that adds one more header would break it again with
      # the same symptom that names nothing.
      add_header Access-Control-Allow-Headers $http_access_control_request_headers always;
      add_header Access-Control-Max-Age       86400 always;
      return 204;
    }
    # Only the preflight is short-circuited. The real request still reaches
    # GoTrue, which sets its own Access-Control-Allow-Origin on the response, so
    # no header is emitted twice.
    proxy_pass http://supabase-auth:9999/;
  }
  location /rest/v1/ { proxy_pass http://supabase-rest:3000/; }
  location = /healthz { return 200 "ok\n"; }
}
NGINX
docker rm -f supabase-gw >/dev/null 2>&1 || true
docker run -d --name supabase-gw --network "$network" -p "${gateway_port}:80" \
  -e "GW_CONF=$gw_conf" \
  --entrypoint /bin/sh \
  nginx:1.27-alpine \
  -c 'printf "%s\n" "$GW_CONF" > /etc/nginx/conf.d/default.conf && exec nginx -g "daemon off;"' >/dev/null

base="http://localhost:${gateway_port}"
for _ in $(seq 1 60); do
  code="$(curl -s -o /dev/null -w '%{http_code}' "$base/rest/v1/tenants?select=id&limit=1" \
    -H "apikey: $service_role_key" -H "Authorization: Bearer $service_role_key" || true)"
  [ "$code" = "200" ] && break
  sleep 2
done
if [ "$code" != "200" ]; then
  log "::error::PostgREST never served public.tenants through the gateway (last status $code)"
  docker logs supabase-rest 2>&1 | tail -30 >&2
  exit 1
fi
log "PostgREST is serving the migrated schema through the gateway"

# ---------------------------------------------------------------------------
# CORS preflight, asserted here rather than discovered in Playwright.
#
# Everything above this line can be healthy while the browser is still unable to
# make a single call, because a preflight refusal is invisible to curl: the two
# readiness checks above pass, the stack reports itself up, and the failure then
# surfaces twenty minutes later as a navigation timeout inside signIn(), with
# the real error already swallowed by the console's generic auth copy. That is
# what happened, so this asserts the browser's own request shape at the point
# where a failure can still name its cause.
#
# The request shape is not arbitrary. Per the Fetch spec a browser sends
# Access-Control-Request-Headers lowercased and lexicographically sorted, and
# rs/cors (which GoTrue uses) relies on that ordering, so an unsorted probe
# gets answers a browser would never get. `apikey` is in the list because
# supabase-js always sends it, and it is the header GoTrue rejects.
# ---------------------------------------------------------------------------
browser_request_headers='apikey,authorization,content-type,x-client-info,x-supabase-api-version'
preflight_origin='http://localhost:3000'

# Every check below is on an exact token, never a substring. `grep -q apikey`
# would be satisfied by `x-vendor-apikey-hint`, and a check that can be
# satisfied by something other than the thing it names is a check that cannot
# reliably go red.
header_value() {
  printf '%s\n' "$1" | grep -i "^$2:" | head -1 | cut -d: -f2- | tr -d ' '
}
has_token() {
  printf '%s' "$1" | tr 'A-Z,' 'a-z\n' | grep -qx "$2"
}

assert_preflight() {
  local label="$1" path="$2" method="$3"
  local dump status headers allow_origin allow_headers allow_methods
  dump="$(mktemp)"
  status="$(curl -s -o /dev/null -D "$dump" -w '%{http_code}' -X OPTIONS "${base}${path}" \
    -H "Origin: ${preflight_origin}" \
    -H "Access-Control-Request-Method: ${method}" \
    -H "Access-Control-Request-Headers: ${browser_request_headers}" || true)"
  headers="$(tr -d '\r' < "$dump")"
  rm -f "$dump"

  # A preflight is only successful on a 2xx. A 404 or a 502 can still carry an
  # Access-Control-Allow-Origin (nginx `add_header ... always` emits it on error
  # responses too), so checking the header without the status would pass on a
  # gateway that is routing the preflight nowhere at all.
  case "$status" in
    2??) ;;
    *)
      log "::error::${label} answered the browser CORS preflight with HTTP ${status}, which a browser treats as a failed preflight."
      printf '%s\n' "$headers" >&2
      return 1
      ;;
  esac

  allow_origin="$(header_value "$headers" 'access-control-allow-origin')"
  allow_headers="$(header_value "$headers" 'access-control-allow-headers')"
  allow_methods="$(header_value "$headers" 'access-control-allow-methods')"

  if [ -z "$allow_origin" ]; then
    log "::error::${label} refused the browser CORS preflight: no Access-Control-Allow-Origin in the response. Every supabase-js call from the browser will fail with 'Failed to fetch' before it is sent."
    printf '%s\n' "$headers" >&2
    return 1
  fi
  # A non-empty value is not the same as a usable one: a browser rejects any
  # origin that is neither the wildcard nor its own.
  if [ "$allow_origin" != "*" ] && [ "$allow_origin" != "$preflight_origin" ]; then
    log "::error::${label} allowed the origin '${allow_origin}', which is neither '*' nor '${preflight_origin}', so the browser blocks the request anyway."
    return 1
  fi
  if ! has_token "$allow_methods" "$(printf '%s' "$method" | tr 'A-Z' 'a-z')"; then
    log "::error::${label} answered the preflight but did not allow the ${method} method. Got: ${allow_methods:-<none>}"
    return 1
  fi
  # Presence of the origin header is not enough. A preflight can be answered
  # while still omitting the one header supabase-js cannot do without, and the
  # browser blocks that request just as completely.
  if ! has_token "$allow_headers" 'apikey'; then
    log "::error::${label} answered the preflight but did not allow the 'apikey' header, which supabase-js sends on every request. Got: ${allow_headers:-<none>}"
    return 1
  fi
  log "${label} accepts the browser CORS preflight, apikey included"
  return 0
}

preflight_failures=0
assert_preflight "GoTrue (/auth/v1)"   "/auth/v1/token?grant_type=password" POST || preflight_failures=$((preflight_failures + 1))
assert_preflight "PostgREST (/rest/v1)" "/rest/v1/tenants?select=id"        GET  || preflight_failures=$((preflight_failures + 1))
if [ "$preflight_failures" -ne 0 ]; then
  log "::error::the gateway is up but unusable from a browser; not handing these values to a job that will only fail inside Playwright"
  exit 1
fi

# The gateway address a container on another docker network reaches. Not
# localhost: inside those containers that is the container itself.
gw_ip="$(docker network inspect bridge -f '{{ (index .IPAM.Config 0).Gateway }}')"

echo "SUPABASE_URL=${base}"
echo "SUPABASE_ANON_KEY=${anon_key}"
echo "SUPABASE_SERVICE_ROLE_KEY=${service_role_key}"
echo "SUPABASE_URL_FROM_CONTAINER=http://${gw_ip}:${gateway_port}"

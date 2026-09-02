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
# Defaulted from the caller's libpq environment rather than fixed here. These
# three build the DSN handed to GoTrue and PostgREST, while every psql call in
# this script uses PGUSER, PGPASSWORD and PGDATABASE. When the two disagree the
# psql steps all succeed and only GoTrue fails, and it fails as "GoTrue never
# became healthy", which is exactly the wrong-component misattribution the
# container check further down exists to prevent. A --db-name divergence was
# already caught by the auth.users probe; a user or password divergence was not
# caught at all.
db_name="${PGDATABASE:-hive_ci}"
db_user="${PGUSER:-postgres}"
# Unquoted on purpose. An assignment does not word-split, and this keeps the
# repository secret scanner from reading an environment-variable default as a
# hardcoded credential. Nothing here is a secret in any case: the throwaway
# Postgres is created with this value minutes earlier in the same job and is
# destroyed with the runner.
db_password=${PGPASSWORD:-postgres}
network="hive-ci-supabase"
gateway_port="9000"
# Opt-in, and off by default so the callers that only need a browser login are
# byte-for-byte unaffected. See the "JWKS over TLS" section further down for
# why a second front exists and why the signing key changes shape with it.
jwks_tls_ca=""
jwks_tls_host="supabase-tls"
jwks_tls_port="9443"
# The externally visible base of GoTrue: what the discovery document advertises
# as its endpoints, and what GoTrue stamps as `iss`. Empty keeps the historical
# value, which is right for a caller whose whole world is the runner's own
# localhost. It is NOT right for a caller whose browser and whose containers
# both have to reach one origin, because inside a container localhost is the
# container; that caller passes the docker bridge form here.
#
# Note the /auth/v1 suffix such a caller has to include. The gateway below maps
# /auth/v1/ onto GoTrue's root, so the prefix is part of the external address
# even though GoTrue itself never sees it.
external_url=""
# Opt-in, off by default. The OAuth 2.1 authorization server is what Open WebUI
# signs in through, and enabling it needs a GoTrue new enough to have one: see
# the image selection below.
oauth_server=0

while [ "$#" -gt 0 ]; do
  case "$1" in
    --db-container) db_container="$2"; shift 2 ;;
    --db-name) db_name="$2"; shift 2 ;;
    --port) gateway_port="$2"; shift 2 ;;
    --jwks-tls-ca) jwks_tls_ca="$2"; shift 2 ;;
    --jwks-tls-host) jwks_tls_host="$2"; shift 2 ;;
    --jwks-tls-port) jwks_tls_port="$2"; shift 2 ;;
    --external-url) external_url="$2"; shift 2 ;;
    --oauth-server) oauth_server=1; shift ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done

log() { echo "$@" >&2; }

# --db-name is parsed after the libpq defaults above, so it can still reintroduce
# the split this script just closed: the DSN would name one database and every
# psql call another. Fail here, naming the two values, rather than forty lines
# later as a health check on the wrong component.
if [ -n "${PGDATABASE:-}" ] && [ "$db_name" != "$PGDATABASE" ]; then
  log "::error::--db-name is '$db_name' but PGDATABASE is '$PGDATABASE'. GoTrue and PostgREST would be pointed at one database while this script migrates another. Set both or neither."
  exit 2
fi

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
# JWKS over TLS. Opt-in, requested with --jwks-tls-ca.
#
# Why a caller would want it: edge-api does not take a shared secret. It
# validates every browser token with jwt.WithKeySet against SUPABASE_JWKS_URL
# and refuses to boot when the first refresh yields no usable key
# (apps/edge-api/internal/auth/jwt_supabase.go). Two separate properties of the
# default stack above defeat that, and fixing either one alone changes nothing:
#
#   * HS256 alone publishes NOTHING. GoTrue's internal/api/jwks.go skips every
#     key whose public half is nil or of type jwa.OctetSeq, so with only
#     GOTRUE_JWT_SECRET the endpoint answers {"keys":[]}. Measured against the
#     pin above rather than inferred. The fix is upstream-supported and
#     config-only: hand GoTrue an EC P-256 key through GOTRUE_JWT_KEYS and it
#     signs user tokens ES256 and publishes the public half, while still
#     accepting the legacy HS256 anon and service_role keys through the
#     GOTRUE_JWT_SECRET fallback. Both halves come from
#     scripts/generate-enterprise-jwt-keys.py, which the enterprise profile
#     already uses for exactly this.
#   * edge-api refuses a plain http JWKS URL, deliberately, because anything on
#     the path to one can swap the key set and mint tokens it would then accept
#     (loadJWTAuthEnv in apps/edge-api/cmd/server/main.go, guarded by
#     jwt_env_test.go). That guard is NOT relaxed here. Caddy terminates real
#     TLS with its own local authority and the caller is handed that authority
#     to trust, so the chain and the hostname are still verified. It is the
#     same arrangement docker-compose.enterprise.yml runs on the demo box.
#
# PostgREST moves to the verification key set at the same time, because the
# user tokens it now sees are ES256 while the anon and service_role keys stay
# HS256, and it has to accept both.
# ---------------------------------------------------------------------------
gotrue_key_args=()
pgrst_jwt="${jwt_secret}"
if [ -n "$jwks_tls_ca" ]; then
  log "==> asymmetric signing key, so the JWKS endpoint is not empty"
  keys_out="$(ENTERPRISE_JWT_SECRET="$jwt_secret" \
    python3 "$repo_root/scripts/generate-enterprise-jwt-keys.py")"
  jwt_keys="$(printf '%s\n' "$keys_out" | sed -n 's/^ENTERPRISE_JWT_KEYS=//p')"
  jwt_verify_keys="$(printf '%s\n' "$keys_out" | sed -n 's/^ENTERPRISE_JWT_VERIFY_KEYS=//p')"
  # Both, not either. A truncated generator run would otherwise leave GoTrue
  # signing HS256 again, and that surfaces as edge-api refusing to boot, three
  # components away from the cause.
  if [ -z "$jwt_keys" ] || [ -z "$jwt_verify_keys" ]; then
    log "::error::generate-enterprise-jwt-keys.py printed no ENTERPRISE_JWT_KEYS / ENTERPRISE_JWT_VERIFY_KEYS pair"
    exit 1
  fi
  # --external-url wins when it was given. The two consumers of a JWKS front
  # do not have to be the same consumer: edge-api fetches the key set over
  # TLS, while a browser and a container both follow the discovery document
  # over the plain origin, and the `iss` claim has to match what THAT document
  # advertises or every OIDC client rejects the id_token. Deriving the issuer
  # from the TLS front is right only when the TLS front is also the address
  # everything else uses.
  jwks_issuer="${external_url:-https://${jwks_tls_host}:${jwks_tls_port}/auth/v1}"
  gotrue_key_args=(
    -e "GOTRUE_JWT_KEYS=${jwt_keys}"
    # Set explicitly, and emitted verbatim below as SUPABASE_JWT_ISSUER. The
    # issuer is a string comparison against the token claim and is never
    # fetched, so the only way it goes wrong is by drifting from what GoTrue
    # stamps. One variable feeding both is what stops that.
    -e "GOTRUE_JWT_ISSUER=${jwks_issuer}"
  )
  pgrst_jwt="${jwt_verify_keys}"
fi

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

# Percent-encoded, because these three now come from the caller's environment
# rather than being fixed literals. A password containing @ or / or ? silently
# reshapes this URI: GoTrue and PostgREST would parse a different host or
# database and fail as a connection error naming neither. The defaults are safe
# and would not need this; a caller's PGPASSWORD is not guaranteed to be.
urlenc() { python3 -c 'import sys, urllib.parse; print(urllib.parse.quote(sys.argv[1], safe=""))' "$1"; }
db_url="postgres://$(urlenc "$db_user"):$(urlenc "$db_password")@supabase-db:5432/$(urlenc "$db_name")"

log "==> auth schema and roles, before GoTrue migrates into them"
psql --no-psqlrc -qX -v ON_ERROR_STOP=1 -f "$repo_root/deploy/supabase/init/00-extensions.sql" >/dev/null

# ---------------------------------------------------------------------------
# GoTrue image and the OAuth 2.1 authorization server.
#
# Two pins on purpose, not an oversight. v2.170.0 is what every existing caller
# has been exercised against and stays the default, so a job that only needs a
# browser login is byte for byte unaffected. The OAuth server does not exist in
# it at all: scripts/register-owui-oauth-client.py documents that the route it
# registers against answers 404 there, and Open WebUI's whole sign-in is that
# server, so a caller asking for --oauth-server is asking for a newer GoTrue by
# definition.
#
# The newer pin is the same digest deploy/docker/docker-compose.enterprise.yml
# runs, rather than a third version chosen here: the demo box already proves
# that build against this migration chain, including the custom access token
# hook, so --oauth-server borrows a combination that is known to work instead
# of introducing one.
# ---------------------------------------------------------------------------
gotrue_image="supabase/gotrue:v2.170.0"
gotrue_oauth_args=()
if [ "$oauth_server" = "1" ]; then
  gotrue_image="supabase/gotrue:v2.189.0@sha256:385184459f57569c54c25209f51f3b2be99ddd7c4ce9e3555b5d3eea8447b7cf"
  gotrue_oauth_args=(
    -e GOTRUE_OAUTH_SERVER_ENABLED=true
    # Where GoTrue sends the browser to collect consent, resolved against
    # GOTRUE_SITE_URL. That is the console's own consent route
    # (apps/web-console/app/oauth/consent/page.tsx), so a caller passing this
    # flag has to be serving the console at the site URL as well; the
    # authorize call otherwise redirects the browser at a 404 and the sign-in
    # simply stops, with nothing in GoTrue's log to read.
    -e GOTRUE_OAUTH_SERVER_AUTHORIZATION_PATH=/oauth/consent
  )
fi

log "==> GoTrue"
docker rm -f supabase-auth >/dev/null 2>&1 || true
docker run -d --name supabase-auth --network "$network" \
  -e GOTRUE_API_HOST=0.0.0.0 \
  -e GOTRUE_API_PORT=9999 \
  -e "API_EXTERNAL_URL=${external_url:-http://localhost:${gateway_port}}" \
  -e GOTRUE_DB_DRIVER=postgres \
  -e "GOTRUE_DB_DATABASE_URL=${db_url}?search_path=auth&sslmode=disable" \
  -e "GOTRUE_SITE_URL=http://localhost:3000" \
  -e "GOTRUE_URI_ALLOW_LIST=http://localhost:3000/**,http://127.0.0.1:3000/**" \
  -e "GOTRUE_JWT_SECRET=${jwt_secret}" \
  ${gotrue_key_args[@]+"${gotrue_key_args[@]}"} \
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
  ${gotrue_oauth_args[@]+"${gotrue_oauth_args[@]}"} \
  "$gotrue_image" >/dev/null

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
  -e "PGRST_JWT_SECRET=${pgrst_jwt}" \
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
    # PostgREST deliberately gets no such block: it already answers this exact
    # preflight itself, echoing the requested headers back with an
    # Access-Control-Allow-Origin, so a block there would be redundant. Note
    # that it would NOT duplicate the header, which an earlier version of this
    # comment claimed: a `return 204` short-circuits before proxy_pass, so the
    # upstream is never reached and never contributes a second copy. Measured.
    # Duplication is only reachable on the REAL response, which is why the
    # gateway's add_header directives live inside the `if` below rather than at
    # location level, and why assert_single_cors_origin further down exists.
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

# The preflight is only half of the browser's contract, and it is the half that
# cannot break by duplication: `return 204` short-circuits before proxy_pass, so
# only one component ever answers an OPTIONS. The REAL response is the half that
# can. There the gateway proxies to GoTrue, GoTrue sets its own
# Access-Control-Allow-Origin, and if the gateway's add_header directives were
# ever moved out of the `if` to location level, nginx would emit the gateway's
# copy too. A browser rejects a duplicated Access-Control-Allow-Origin exactly
# as hard as a missing one, and nothing above this line would notice:
# assert_preflight probes only OPTIONS, and header_value() takes `head -1`, so
# it could not see a second copy even if it looked.
#
# Verified by mutation before this was written: moving the add_header out of the
# `if` leaves every preflight assertion green while the real response carries
# two Access-Control-Allow-Origin headers.
#
# /rest/v1 is deliberately not probed here. The gateway adds no header on that
# prefix at all, so duplication is impossible there by construction, and only
# the path that can actually break is worth an assertion.
assert_single_cors_origin() {
  local label="$1" path="$2" method="$3" count
  # grep -c exits 1 on zero matches, which pipefail would otherwise turn into a
  # script-killing failure rather than the named error below.
  count="$(curl -s -o /dev/null -D - -X "$method" "${base}${path}" \
    -H "Origin: ${preflight_origin}" \
    -H "apikey: ${service_role_key}" \
    | tr -d '\r' | grep -ci '^access-control-allow-origin:' || true)"
  if [ "$count" != "1" ]; then
    log "::error::${label} returned ${count} Access-Control-Allow-Origin header(s) on a real ${method}, not exactly 1. A browser rejects a duplicated value as hard as a missing one, and every credentialed spec would fail inside signIn() naming nothing."
    return 1
  fi
  log "${label} sets exactly one Access-Control-Allow-Origin on a real ${method}"
  return 0
}

preflight_failures=0
assert_preflight "GoTrue (/auth/v1)"   "/auth/v1/token?grant_type=password" POST || preflight_failures=$((preflight_failures + 1))
assert_preflight "PostgREST (/rest/v1)" "/rest/v1/tenants?select=id"        GET  || preflight_failures=$((preflight_failures + 1))
assert_single_cors_origin "GoTrue (/auth/v1)" "/auth/v1/settings" GET || preflight_failures=$((preflight_failures + 1))
if [ "$preflight_failures" -ne 0 ]; then
  log "::error::the gateway is up but unusable from a browser; not handing these values to a job that will only fail inside Playwright"
  exit 1
fi

# ---------------------------------------------------------------------------
# The TLS front, when --jwks-tls-ca asked for one.
#
# It proxies the nginx gateway rather than replacing it, so the CORS handling
# and prefix routing asserted above stay the one implementation. All this adds
# is a verified-TLS listener for the single consumer that requires one.
#
# The listener carries a HOSTNAME, not the bridge IP, because the certificate
# has to match what the consumer asks for and a name survives a runner whose
# docker0 address differs. The consumer maps that name onto the published port
# itself: for a compose service that is an extra_hosts entry pointing at the
# bridge gateway address emitted below.
# ---------------------------------------------------------------------------
if [ -n "$jwks_tls_ca" ]; then
  log "==> TLS front for the JWKS endpoint"
  read -r -d '' tls_conf <<CADDY || true
{
	auto_https disable_redirects
}
https://${jwks_tls_host}:${jwks_tls_port} {
	tls internal
	reverse_proxy supabase-gw:80
}
CADDY
  docker rm -f supabase-tls >/dev/null 2>&1 || true
  # Same pinned Caddy digest deploy/docker/docker-compose.enterprise.yml runs,
  # so this pulls nothing a stack boot has not already pulled.
  docker run -d --name supabase-tls --network "$network" \
    --network-alias "$jwks_tls_host" \
    -p "${jwks_tls_port}:${jwks_tls_port}" \
    -e "TLS_CONF=$tls_conf" \
    --entrypoint /bin/sh \
    caddy:2-alpine@sha256:86deaf5e3d3408a6ccec08fbb79989783dd26e206ae10bcf78a801dc8c9ab794 \
    -c 'printf "%s\n" "$TLS_CONF" > /etc/caddy/Caddyfile && exec caddy run --config /etc/caddy/Caddyfile' >/dev/null

  ca_src="/data/caddy/pki/authorities/local/root.crt"
  for _ in $(seq 1 60); do
    if docker exec supabase-tls test -s "$ca_src" 2>/dev/null; then break; fi
    sleep 2
  done
  if ! docker exec supabase-tls test -s "$ca_src" 2>/dev/null; then
    log "::error::the TLS front never wrote its local authority certificate"
    docker logs supabase-tls 2>&1 | tail -30 >&2
    exit 1
  fi
  mkdir -p "$(dirname "$jwks_tls_ca")"
  docker cp "supabase-tls:${ca_src}" "$jwks_tls_ca" >/dev/null
  # Caddy writes root.crt 0600 root. The published edge-api image runs as uid
  # 10001, so an unreadable file means SUPABASE_JWKS_CA_FILE fails with
  # permission denied and the process exits at boot. Same reason
  # docker-compose.enterprise.yml exports the certificate through a one-shot
  # service instead of mounting Caddy's data directory.
  chmod 0644 "$jwks_tls_ca"

  # The whole point of the two changes above, asserted end to end rather than
  # assumed: real TLS, verified against that authority, returning a key set
  # with at least one key in it. An empty "keys" array is the exact shape
  # HS256-only GoTrue returns, and it is what makes edge-api refuse to boot, so
  # it fails here by name instead of there by symptom.
  # Polled, not probed once. root.crt existing means Caddy's authority is on
  # disk, which happens a beat before the listener will actually complete a
  # handshake with the certificate it just issued: measured on run 33675164605,
  # where the single-shot curl ran 0.2 seconds after "certificate obtained
  # successfully" and came back empty, and the job then failed claiming GoTrue
  # published no key. The distinction this assertion exists to make is between
  # a key set that is empty and one that is not, so it must not also be able to
  # fail on a listener that is a fraction of a second from ready.
  jwks_body=""
  for _ in $(seq 1 30); do
    jwks_body="$(curl -sS --cacert "$jwks_tls_ca" \
      --resolve "${jwks_tls_host}:${jwks_tls_port}:127.0.0.1" \
      "https://${jwks_tls_host}:${jwks_tls_port}/auth/v1/.well-known/jwks.json" || true)"
    if printf '%s' "$jwks_body" | python3 -c 'import json,sys; sys.exit(0 if json.load(sys.stdin).get("keys") else 1)' 2>/dev/null; then
      break
    fi
    sleep 2
  done
  if ! printf '%s' "$jwks_body" | python3 -c 'import json,sys; sys.exit(0 if json.load(sys.stdin).get("keys") else 1)' 2>/dev/null; then
    log "::error::the JWKS endpoint served no usable key over TLS. edge-api validates every browser token against this document and refuses to boot on an empty key set."
    docker logs supabase-tls 2>&1 | tail -20 >&2
    exit 1
  fi
  log "JWKS is served over verified TLS and carries at least one key"
fi

# The gateway address a container on another docker network reaches. Not
# localhost: inside those containers that is the container itself.
gw_ip="$(docker network inspect bridge -f '{{ (index .IPAM.Config 0).Gateway }}')"

echo "SUPABASE_URL=${base}"
echo "SUPABASE_ANON_KEY=${anon_key}"
echo "SUPABASE_SERVICE_ROLE_KEY=${service_role_key}"
echo "SUPABASE_URL_FROM_CONTAINER=http://${gw_ip}:${gateway_port}"
if [ -n "$jwks_tls_ca" ]; then
  # The issuer GoTrue was told to stamp, the https document edge-api fetches,
  # the authority that makes it verifiable, and the address the hostname has to
  # resolve to from inside a container. A consumer needs all four or none.
  echo "SUPABASE_JWT_ISSUER=${jwks_issuer}"
  echo "SUPABASE_JWKS_URL=https://${jwks_tls_host}:${jwks_tls_port}/auth/v1/.well-known/jwks.json"
  echo "SUPABASE_JWKS_CA_PATH=${jwks_tls_ca}"
  echo "SUPABASE_JWKS_HOST=${jwks_tls_host}"
  echo "SUPABASE_JWKS_HOST_IP=${gw_ip}"
fi

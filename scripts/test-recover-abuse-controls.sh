#!/usr/bin/env bash
# Runtime proof for the /auth/v1/recover abuse controls of issue #1744.
#
# Everything this exercises is configuration, and configuration nobody ran is
# not a fix. The two claims under test cannot be read off a Caddyfile:
#
#   1. The GoTrue rate-limit bucket is keyed on the real client address, so one
#      caller exhausting its quota does not deny password reset to anybody
#      else. Before the fix the key was the console proxy's container address
#      and 30 requests from one host 429'd the entire deployment for an hour.
#   2. One target address cannot be mailed more than once per
#      GOTRUE_SMTP_MAX_FREQUENCY window no matter how many source addresses the
#      requests come from.
#
# and the property both changes have to preserve:
#
#   3. POST /auth/v1/recover answers 200 {} for an address that holds an
#      account, an address that does not, and an address that is being
#      throttled. A status that varied would read the account list out loud.
#
# It stands up the REAL Caddyfiles (deploy/docker/Caddyfile.console and
# Caddyfile.supabase, mounted, not copied) in front of the same digest-pinned
# GoTrue the enterprise stack runs, with Postgres behind it and MailHog as the
# relay so delivered mail can be counted. Nothing here touches a deployed
# environment: the audit that filed #1744 measured the defect by probing the
# live box and burned its hourly email budget doing it.
#
# Usage: scripts/test-recover-abuse-controls.sh
# Requires: docker, python3. Takes about a minute, most of it image pulls.

set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
NET="hive1744-$$"
PREFIX="hive1744-$$"

# Same digests the enterprise stack pins, so what is proven here is what runs
# there. Postgres and MailHog are test-only scaffolding and float on a tag.
GOTRUE_IMAGE='supabase/gotrue:v2.189.0@sha256:385184459f57569c54c25209f51f3b2be99ddd7c4ce9e3555b5d3eea8447b7cf'
CADDY_IMAGE='caddy:2-alpine@sha256:86deaf5e3d3408a6ccec08fbb79989783dd26e206ae10bcf78a801dc8c9ab794'
PG_IMAGE='postgres:16-alpine'
MAIL_IMAGE='mailhog/mailhog:v1.0.1'

JWT_SECRET='test-only-jwt-secret-for-1744-abuse-control-harness'
DB_URL="postgres://postgres:postgres@${PREFIX}-pg:5432/postgres?sslmode=disable&search_path=auth"

cleanup() {
  docker rm -f "${PREFIX}-console" "${PREFIX}-supabase" "${PREFIX}-auth" \
    "${PREFIX}-mail" "${PREFIX}-pg" >/dev/null 2>&1 || true
  docker network rm "$NET" >/dev/null 2>&1 || true
}
trap cleanup EXIT

echo "== pulling images"
for image in "$GOTRUE_IMAGE" "$CADDY_IMAGE" "$PG_IMAGE" "$MAIL_IMAGE"; do
  docker image inspect "$image" >/dev/null 2>&1 || docker pull -q "$image"
done

docker network create "$NET" >/dev/null

echo "== postgres"
docker run -d --name "${PREFIX}-pg" --network "$NET" --network-alias "${PREFIX}-pg" \
  -e POSTGRES_PASSWORD=postgres -e POSTGRES_DB=postgres "$PG_IMAGE" >/dev/null
for _ in $(seq 1 60); do
  docker exec "${PREFIX}-pg" pg_isready -U postgres >/dev/null 2>&1 && break
  sleep 1
done
docker exec "${PREFIX}-pg" psql -U postgres -c 'CREATE SCHEMA IF NOT EXISTS auth' >/dev/null

echo "== mailhog"
docker run -d --name "${PREFIX}-mail" --network "$NET" --network-alias "${PREFIX}-mail" \
  -p 127.0.0.1:8925:8025 "$MAIL_IMAGE" >/dev/null

# GoTrue's environment. Only the values this test depends on are spelled out;
# the rest of the enterprise service's environment is irrelevant to /recover.
#
# GOTRUE_RATE_LIMIT_OTP is the limit /recover is wrapped in
# (newLimiterPer5mOver1h, api.go), and its burst is hardcoded to 30 in GoTrue
# regardless of this value, which is why the loops below send 31 requests.
GOTRUE_ENV=(
  -e GOTRUE_API_HOST=0.0.0.0
  -e GOTRUE_API_PORT=9999
  -e "API_EXTERNAL_URL=http://localhost:9999"
  -e GOTRUE_DB_DRIVER=postgres
  -e "GOTRUE_DB_DATABASE_URL=${DB_URL}"
  -e "DATABASE_URL=${DB_URL}"
  -e "GOTRUE_SITE_URL=http://console.localhost"
  -e "GOTRUE_JWT_SECRET=${JWT_SECRET}"
  -e GOTRUE_JWT_ADMIN_ROLES=service_role
  -e GOTRUE_JWT_AUD=authenticated
  -e GOTRUE_JWT_DEFAULT_GROUP_NAME=authenticated
  -e GOTRUE_JWT_EXP=3600
  -e GOTRUE_DISABLE_SIGNUP=true
  -e GOTRUE_MAILER_AUTOCONFIRM=true
  -e GOTRUE_EXTERNAL_EMAIL_ENABLED=true
  -e "GOTRUE_SMTP_HOST=${PREFIX}-mail"
  -e GOTRUE_SMTP_PORT=1025
  -e "GOTRUE_SMTP_ADMIN_EMAIL=noreply@test.invalid"
  -e "GOTRUE_SMTP_SENDER_NAME=Hive Test"
  -e GOTRUE_MAILER_URLPATHS_RECOVERY=/auth/v1/verify
  -e GOTRUE_RATE_LIMIT_HEADER=X-Forwarded-For
  -e GOTRUE_RATE_LIMIT_OTP=30
  -e GOTRUE_SMTP_MAX_FREQUENCY=5m
  -e GOTRUE_LOG_LEVEL=warn
)

echo "== gotrue schema"
docker run --rm --network "$NET" "${GOTRUE_ENV[@]}" "$GOTRUE_IMAGE" auth migrate >/dev/null

echo "== gotrue"
docker run -d --name "${PREFIX}-auth" --network "$NET" --network-alias supabase-auth \
  -p 127.0.0.1:8999:9999 "${GOTRUE_ENV[@]}" "$GOTRUE_IMAGE" >/dev/null

echo "== caddy-supabase (deploy/docker/Caddyfile.supabase, mounted)"
docker run -d --name "${PREFIX}-supabase" --network "$NET" --network-alias caddy-supabase \
  -e SUPABASE_DOMAIN=supabase.localhost \
  -v "${HERE}/deploy/docker/Caddyfile.supabase:/etc/caddy/Caddyfile:ro" \
  "$CADDY_IMAGE" >/dev/null

echo "== caddy-console (deploy/docker/Caddyfile.console, mounted)"
docker run -d --name "${PREFIX}-console" --network "$NET" --network-alias caddy-console \
  -e CONSOLE_DOMAIN=console.localhost -e SUPABASE_DOMAIN=supabase.localhost \
  -e CONSOLE_EXTERNAL_SCHEME=http \
  -p 127.0.0.1:8880:80 \
  -v "${HERE}/deploy/docker/Caddyfile.console:/etc/caddy/Caddyfile:ro" \
  "$CADDY_IMAGE" >/dev/null

for _ in $(seq 1 60); do
  curl -fsS -m 2 -H 'Host: console.localhost' http://127.0.0.1:8880/auth/v1/settings >/dev/null 2>&1 && break
  sleep 1
done

echo "== driving"
CONSOLE_URL=http://127.0.0.1:8880 \
GOTRUE_URL=http://127.0.0.1:8999 \
MAILHOG_URL=http://127.0.0.1:8925 \
JWT_SECRET="$JWT_SECRET" \
  python3 "${HERE}/scripts/recover_abuse_controls_probe.py"

#!/usr/bin/env bash
# Reproduce docs/proof/tenant-model-entitlement/live-transcript.md.
#
# Stands up an isolated scratch Postgres, applies the real migration chain plus
# seed.sql, runs a control-plane built from the current checkout against it, and
# replays the entitlement checks over HTTP. Nothing here touches the shared
# deploy/docker compose project or its ports.
set -euo pipefail

REPO="$(cd "$(dirname "$0")/../../.." && pwd)"
DB=tmvent-db
CP=tmvent-cp
NET=tmvent-net
IMG=tmvent-control-plane:proof
TOKEN=proof-internal-token
ENTITLED=11111111-1111-1111-1111-111111111111
BLOCKED=22222222-2222-2222-2222-222222222222
GREEN=33333333-3333-3333-3333-333333333333

cleanup() {
  docker rm -f "$CP" "$DB" >/dev/null 2>&1 || true
  docker network rm "$NET" >/dev/null 2>&1 || true
}
trap cleanup EXIT

cleanup
docker network create "$NET" >/dev/null

docker run -d --name "$DB" --network "$NET" \
  -e POSTGRES_PASSWORD=postgres -e POSTGRES_DB=hive_proof \
  pgvector/pgvector:pg17 >/dev/null

echo "waiting for postgres..."
until docker exec "$DB" pg_isready -U postgres -d hive_proof >/dev/null 2>&1; do sleep 1; done

docker cp "$REPO/.github/ci/test-db-bootstrap.sql" "$DB:/tmp/bootstrap.sql"
docker exec "$DB" psql -U postgres -d hive_proof -v ON_ERROR_STOP=1 -q -f /tmp/bootstrap.sql
docker cp "$REPO/supabase/migrations" "$DB:/tmp/migrations"
docker exec -e PGUSER=postgres -e PGDATABASE=hive_proof "$DB" \
  bash -c 'set -e; for f in /tmp/migrations/*.sql; do psql -v ON_ERROR_STOP=1 -q -f "$f" >/dev/null; done'
docker cp "$REPO/docs/proof/tenant-model-entitlement/seed.sql" "$DB:/tmp/seed.sql"
docker exec "$DB" psql -U postgres -d hive_proof -v ON_ERROR_STOP=1 -f /tmp/seed.sql
echo "schema + fixture ready"

docker build -q -t "$IMG" -f "$REPO/deploy/docker/Dockerfile.control-plane" "$REPO" >/dev/null
docker run -d --name "$CP" --network "$NET" \
  -e SUPABASE_URL=http://stub.invalid \
  -e SUPABASE_DB_URL="postgresql://postgres:postgres@${DB}:5432/hive_proof?sslmode=disable" \
  -e CONTROL_PLANE_INTERNAL_TOKEN="$TOKEN" \
  -e S3_ENDPOINT=http://stub.invalid:9000 -e S3_ACCESS_KEY=proof -e S3_SECRET_KEY=proof \
  -e S3_REGION=us-east-1 -e S3_BUCKET_FILES=hive-files -e S3_BUCKET_IMAGES=hive-images \
  "$IMG" >/dev/null

echo "waiting for control-plane..."
until docker logs "$CP" 2>&1 | grep -q "control-plane listening"; do sleep 3; done

docker run --rm --network "$NET" \
  -e CP="http://${CP}:8081" -e TOK="$TOKEN" \
  -e ENTITLED="$ENTITLED" -e BLOCKED="$BLOCKED" -e GREEN="$GREEN" \
  --entrypoint sh curlimages/curl:8.7.1 -c '
sel() {
  echo "--- $1"
  echo "POST /internal/routing/select  $2"
  curl -s -X POST "$CP/internal/routing/select" -H "Content-Type: application/json" \
    -H "X-Internal-Token: $TOK" -d "$2" -w "\nHTTP %{http_code}\n"
  echo
}

echo "===== 1. same alias, two tenants ====="
sel "entitled tenant asks for hive-fast (no visibility row: public stays allowed)" \
  "{\"alias_id\":\"hive-fast\",\"tenant_id\":\"$ENTITLED\",\"need_chat_completions\":true,\"need_streaming\":true}"
sel "blocked tenant asks for the SAME alias (visible=false row)" \
  "{\"alias_id\":\"hive-fast\",\"tenant_id\":\"$BLOCKED\",\"need_chat_completions\":true,\"need_streaming\":true}"

echo "===== 2. tenant with zero visibility rows (production-safety case) ====="
sel "greenfield tenant, public alias hive-fast" \
  "{\"alias_id\":\"hive-fast\",\"tenant_id\":\"$GREEN\",\"need_chat_completions\":true,\"need_streaming\":true}"
sel "greenfield tenant, preview alias hive-auto" \
  "{\"alias_id\":\"hive-auto\",\"tenant_id\":\"$GREEN\",\"need_chat_completions\":true}"

echo "===== 3. restricted alias needs an explicit grant ====="
sel "greenfield tenant, restricted alias, no grant" \
  "{\"alias_id\":\"hive-restricted-proof\",\"tenant_id\":\"$GREEN\",\"need_chat_completions\":true}"
sel "entitled tenant, restricted alias, visible=true grant" \
  "{\"alias_id\":\"hive-restricted-proof\",\"tenant_id\":\"$ENTITLED\",\"need_chat_completions\":true}"

echo "===== 4. untenanted principal (API-key shape) is unchanged ====="
sel "no tenant_id at all, hive-fast" \
  "{\"alias_id\":\"hive-fast\",\"need_chat_completions\":true,\"need_streaming\":true}"

echo "===== 5. the admin visibility toggle, end to end ====="
echo "--- console block action: DELETE /internal/catalog/visibility/$ENTITLED/hive-fast"
curl -s -X DELETE "$CP/internal/catalog/visibility/$ENTITLED/hive-fast" \
  -H "X-Internal-Token: $TOK" -w "\nHTTP %{http_code}\n"
echo
sel "same entitled tenant, same alias, immediately after the toggle" \
  "{\"alias_id\":\"hive-fast\",\"tenant_id\":\"$ENTITLED\",\"need_chat_completions\":true,\"need_streaming\":true}"
echo "--- console unblock action: PUT visible=true"
curl -s -X PUT "$CP/internal/catalog/visibility/$ENTITLED/hive-fast" \
  -H "Content-Type: application/json" -H "X-Internal-Token: $TOK" \
  -d "{\"visible\":true}" -w "\nHTTP %{http_code}\n"
echo
sel "same entitled tenant, same alias, after unblocking" \
  "{\"alias_id\":\"hive-fast\",\"tenant_id\":\"$ENTITLED\",\"need_chat_completions\":true,\"need_streaming\":true}"

echo "===== 6. /v1/models source: tenant-scoped catalog snapshot ====="
echo "--- blocked tenant snapshot (hive-fast must be absent)"
curl -s "$CP/internal/catalog/snapshot/tenant/$BLOCKED" -H "X-Internal-Token: $TOK" \
  -w "\nHTTP %{http_code}\n" | tr "," "\n" | grep -E "\"id\"|HTTP"
echo
echo "--- greenfield tenant snapshot (zero rows: hive-fast must be present)"
curl -s "$CP/internal/catalog/snapshot/tenant/$GREEN" -H "X-Internal-Token: $TOK" \
  -w "\nHTTP %{http_code}\n" | tr "," "\n" | grep -E "\"id\"|HTTP"
'

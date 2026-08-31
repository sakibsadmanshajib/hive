#!/usr/bin/env bash
# Reproduce docs/proof/restrict-free-aliases-visibility/live-transcript.md.
#
# Stands up an isolated scratch Postgres, applies the real migration chain
# (which includes 20260831_01_restrict_free_pool_aliases_visibility.sql) plus
# seed.sql, runs a control-plane built from the current checkout against it,
# and replays the entitlement checks over HTTP against the real internal
# routing-select and catalog-snapshot endpoints. Nothing here touches the
# shared deploy/docker compose project or its ports. Modelled directly on
# docs/proof/tenant-model-entitlement/run-proof.sh, same shape, different
# fixture.
set -euo pipefail

REPO="$(cd "$(dirname "$0")/../../.." && pwd)"
DB=freealiasvis-db
CP=freealiasvis-cp
NET=freealiasvis-net
IMG=freealiasvis-control-plane:proof
TOKEN=proof-internal-token
CUSTOMER=c1111111-1111-1111-1111-111111111111
AUTOMATION=a1111111-1111-1111-1111-111111111111

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
docker cp "$REPO/docs/proof/restrict-free-aliases-visibility/seed.sql" "$DB:/tmp/seed.sql"
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
  -e CUSTOMER="$CUSTOMER" -e AUTOMATION="$AUTOMATION" \
  --entrypoint sh curlimages/curl:8.7.1 -c '
sel() {
  echo "--- $1"
  echo "POST /internal/routing/select  $2"
  curl -s -X POST "$CP/internal/routing/select" -H "Content-Type: application/json" \
    -H "X-Internal-Token: $TOK" -d "$2" -w "\nHTTP %{http_code}\n"
  echo
}
snap() {
  echo "--- $1"
  echo "GET /internal/catalog/snapshot/tenant/$2"
  curl -s "$CP/internal/catalog/snapshot/tenant/$2" -H "X-Internal-Token: $TOK" \
    -w "\nHTTP %{http_code}\n" | tr "," "\n" | grep -E "\"id\"|HTTP"
  echo
}

echo "===== 1. an ordinary customer tenant asks for hive-free and hive-free-tools ====="
sel "ordinary customer, hive-free (no visibility grant)" \
  "{\"alias_id\":\"hive-free\",\"tenant_id\":\"$CUSTOMER\",\"need_chat_completions\":true,\"need_streaming\":true}"
sel "ordinary customer, hive-free-tools (no visibility grant)" \
  "{\"alias_id\":\"hive-free-tools\",\"tenant_id\":\"$CUSTOMER\",\"need_chat_completions\":true}"

echo "===== 2. the same two aliases for the automation tenant (visible=true grant, mirrors scripts/ci-seed-api-key.sh) ====="
sel "automation tenant, hive-free (has the grant)" \
  "{\"alias_id\":\"hive-free\",\"tenant_id\":\"$AUTOMATION\",\"need_chat_completions\":true,\"need_streaming\":true}"
sel "automation tenant, hive-free-tools (has the grant)" \
  "{\"alias_id\":\"hive-free-tools\",\"tenant_id\":\"$AUTOMATION\",\"need_chat_completions\":true}"

echo "===== 3. picker source: tenant-scoped catalog snapshot (what GET /v1/models resolves through) ====="
echo "--- ordinary customer snapshot (hive-free and hive-free-tools must both be absent)"
snap "ordinary customer catalog snapshot" "$CUSTOMER"
echo "--- automation tenant snapshot (both must be present)"
snap "automation tenant catalog snapshot" "$AUTOMATION"

echo "===== 4. a public alias is unaffected, same customer tenant ====="
sel "ordinary customer, hive-default (public, no restriction here)" \
  "{\"alias_id\":\"hive-default\",\"tenant_id\":\"$CUSTOMER\",\"need_chat_completions\":true,\"need_streaming\":true}"
'

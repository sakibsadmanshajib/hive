#!/usr/bin/env bash
# Stand up the A/B proof stack and capture it.
#
# Two Open WebUI containers built from the same pinned upstream digest: the
# baseline is this repository's main, the other is this branch. A Caddy in
# front of the branch container, with the repository's own Caddyfile, so the
# agent workspace is reachable at the path the shell links to.
set -euo pipefail

REPO=$(git rev-parse --show-toplevel)
OUT=${OUT_DIR:-$REPO/docs/proof/shell-nav-2026-08-17/out}
# A checkout of main, for the baseline container's own static hooks. Without it
# the baseline would mount this branch's copy, which no longer has the loader
# that injects the launcher being compared against.
MAIN_STATIC=${MAIN_STATIC:-$REPO/../hive-main/deploy/docker/owui-static}
NET=hive-shell-proof

rm -rf "$OUT"
mkdir -p "$OUT"

cleanup() {
  docker rm -f owui-before open-webui agent-console caddy-proof >/dev/null 2>&1 || true
  docker network rm "$NET" >/dev/null 2>&1 || true
}
trap cleanup EXIT
cleanup

docker network create "$NET" >/dev/null

common_env=(
  -e WEBUI_AUTH=False
  -e DEFAULT_USER_ROLE=admin
  -e WEBUI_NAME=Hive
  -e WEBUI_SECRET_KEY=proof-only-not-a-credential
  -e STATIC_DIR=/data/static
  -e ENABLE_OLLAMA_API=False
  -e ENABLE_OPENAI_API=False
  -e OFFLINE_MODE=True
  -e RAG_EMBEDDING_ENGINE=""
  -e SAFE_MODE=False
)

# The baseline gets main's own static hooks, including the loader.js that
# injects the launcher this change replaces. Mounting the branch's copy would
# hide the control being compared against.
docker run -d --name owui-before --network "$NET" \
  "${common_env[@]}" \
  -v "$MAIN_STATIC:/data/static:ro" \
  hive-open-webui:main-baseline >/dev/null

docker run -d --name open-webui --network "$NET" \
  "${common_env[@]}" \
  -v "$REPO/deploy/docker/owui-static:/data/static:ro" \
  hive-open-webui:hive-fork >/dev/null

docker run -d --name agent-console --network "$NET" \
  -e NEXT_PUBLIC_SUPABASE_URL=https://proof.invalid \
  -e NEXT_PUBLIC_SUPABASE_ANON_KEY=proof-anon-key-placeholder \
  -e NEXT_PUBLIC_EDGE_API_BASE_URL=http://edge-api:8080 \
  -e HIVE_EDGE_API_URL=http://edge-api:8080 \
  hive-agent-console:hive-fork-proof >/dev/null

docker run -d --name caddy-proof --network "$NET" \
  -e HIVE_CHAT_EXTERNAL_SCHEME=http \
  -v "$REPO/deploy/docker/Caddyfile.owui:/etc/caddy/Caddyfile:ro" \
  caddy:2-alpine >/dev/null

echo "waiting for both Open WebUI containers"
for host in owui-before open-webui; do
  for _ in $(seq 1 90); do
    if docker run --rm --network "$NET" curlimages/curl:8.10.1 -sf "http://$host:8080/health" >/dev/null 2>&1; then
      echo "  $host up"
      break
    fi
    sleep 2
  done
done

docker run --rm --network "$NET" \
  -v "$(dirname "$0"):/work:ro" \
  -v "$OUT:/out" \
  -e OUT_DIR=/out \
  -e BEFORE_URL=http://owui-before:8080 \
  -e AFTER_URL=http://caddy-proof:80 \
  -w /work \
  mcr.microsoft.com/playwright:v1.51.1-noble \
  bash -lc 'mkdir -p /tmp/cap && cd /tmp/cap && npm i --silent playwright@1.51.1 >/dev/null 2>&1 && cp /work/capture.mjs . && node capture.mjs'

echo "artifacts in $OUT"
ls -la "$OUT"

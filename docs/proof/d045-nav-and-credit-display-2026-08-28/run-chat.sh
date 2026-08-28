#!/usr/bin/env bash
# Stand up the chat surface built from this branch, behind this repository's own
# Caddyfile.owui, and capture the two chat-side findings.
#
# Self-contained on purpose. Open WebUI runs with WEBUI_AUTH=False and
# OFFLINE_MODE=True, so this needs no Supabase, no control plane and no provider
# key, and it therefore does not touch the shared pool or any other agent's
# stack. What it proves is what the built frontend renders and what the proxy
# in front of it does, which is exactly the scope of the two findings.
set -euo pipefail

REPO=$(git rev-parse --show-toplevel)
HERE=$(cd -- "$(dirname -- "$0")" && pwd)
OUT=${OUT_DIR:-$HERE/out}
IMAGE=${OWUI_IMAGE:-hive-open-webui:d045nav}
NET=hive-d045-proof

rm -rf "$OUT"
mkdir -p "$OUT"

cleanup() {
  docker rm -f owui-d045 caddy-proof >/dev/null 2>&1 || true
  docker network rm "$NET" >/dev/null 2>&1 || true
}
trap cleanup EXIT
cleanup

docker network create "$NET" >/dev/null

# The container is named `open-webui` on the network because Caddyfile.owui's
# catch-all names that upstream. Using the repository's real Caddyfile, rather
# than a proof-only one, is the point: the redirect under test is a line in it.
docker run -d --name owui-d045 --network "$NET" --network-alias open-webui \
  -e WEBUI_AUTH=False \
  -e DEFAULT_USER_ROLE=admin \
  -e WEBUI_NAME=Hive \
  -e WEBUI_SECRET_KEY=proof-only-not-a-credential \
  -e ENABLE_OLLAMA_API=False \
  -e ENABLE_OPENAI_API=False \
  -e OFFLINE_MODE=True \
  -e RAG_EMBEDDING_ENGINE="" \
  -e SAFE_MODE=False \
  -v "$REPO/deploy/docker/owui-static:/data/static:ro" \
  -e STATIC_DIR=/data/static \
  "$IMAGE" >/dev/null

docker run -d --name caddy-proof --network "$NET" \
  -e HIVE_CHAT_EXTERNAL_SCHEME=http \
  -v "$REPO/deploy/docker/Caddyfile.owui:/etc/caddy/Caddyfile:ro" \
  caddy:2-alpine >/dev/null

echo "waiting for Open WebUI"
for _ in $(seq 1 120); do
  if docker run --rm --network "$NET" curlimages/curl:8.10.1 -sf http://open-webui:8080/health >/dev/null 2>&1; then
    echo "  up"
    break
  fi
  sleep 2
done

# The redirect, read at the protocol level before any browser is involved.
{
  echo "curl through the repository Caddyfile (deploy/docker/Caddyfile.owui):"
  for path in /agent-workspace /agent-workspace/ /agent-workspace/tasks \
              /agent-workspace/auth/sign-in /agents /agent-workspaces; do
    code=$(docker run --rm --network "$NET" curlimages/curl:8.10.1 \
      -s -o /dev/null -w '%{http_code} %{redirect_url}' "http://caddy-proof:80$path" || true)
    printf '  %-32s -> %s\n' "$path" "$code"
  done
} | tee "$OUT/redirect.log"

docker run --rm --network "$NET" \
  -v "$HERE:/work:ro" \
  -v "$OUT:/out" \
  -e OUT_DIR=/out \
  -e CHAT_URL=http://caddy-proof:80 \
  -w /work \
  mcr.microsoft.com/playwright:v1.51.1-noble \
  bash -lc 'mkdir -p /tmp/cap && cd /tmp/cap && npm i --silent playwright@1.51.1 >/dev/null 2>&1 && cp /work/capture-chat.mjs . && node capture-chat.mjs'

echo "artifacts in $OUT"
ls -la "$OUT"

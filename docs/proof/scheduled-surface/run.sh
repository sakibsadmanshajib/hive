#!/usr/bin/env bash
# Stand up a throwaway Open WebUI container from this branch's proof image and
# capture the Scheduled nav row plus the /schedules empty state.
#
# Auth is disabled so no fixture or shared account is touched; the container
# runs OFFLINE_MODE with no provider keys. Everything dies on exit.
set -euo pipefail

REPO=$(git rev-parse --show-toplevel)
HERE=$(dirname "$0")
OUT=${OUT_DIR:-$REPO/docs/proof/scheduled-surface/out}
NET=schedproof-net

mkdir -p "$OUT"

cleanup() {
  docker rm -f schedproof-owui >/dev/null 2>&1 || true
  docker network rm "$NET" >/dev/null 2>&1 || true
}
trap cleanup EXIT
cleanup

docker network create "$NET" >/dev/null

# Same posture as docs/proof/shell-nav-2026-08-17/run.sh: auth off, offline,
# no providers, static dir mounted from this tree.
docker run -d --name schedproof-owui --network "$NET" \
  -e WEBUI_AUTH=False \
  -e DEFAULT_USER_ROLE=admin \
  -e WEBUI_NAME=Hive \
  -e WEBUI_SECRET_KEY=proof-only-not-a-credential \
  -e STATIC_DIR=/data/static \
  -e ENABLE_OLLAMA_API=False \
  -e ENABLE_OPENAI_API=False \
  -e OFFLINE_MODE=True \
  -e RAG_EMBEDDING_ENGINE="" \
  -e SAFE_MODE=False \
  hive-open-webui:scheduled-proof >/dev/null

echo "waiting for open-webui"
for _ in $(seq 1 90); do
  if docker run --rm --network "$NET" curlimages/curl:8.10.1 -sf "http://schedproof-owui:8080/health" >/dev/null 2>&1; then
    echo "  up"
    break
  fi
  sleep 2
done

docker run --rm --network "$NET" \
  -v "$(cd "$HERE" && pwd):/work:ro" \
  -v "$OUT:/out" \
  -e OUT_DIR=/out \
  -e BASE_URL=http://schedproof-owui:8080 \
  -w /work \
  mcr.microsoft.com/playwright:v1.51.1-noble \
  bash -lc 'mkdir -p /tmp/cap && cd /tmp/cap && npm i --silent playwright@1.51.1 >/dev/null 2>&1 && cp /work/capture.mjs . && node capture.mjs'

echo "artifacts in $OUT"
ls -la "$OUT"

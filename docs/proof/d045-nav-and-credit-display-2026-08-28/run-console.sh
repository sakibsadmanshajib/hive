#!/usr/bin/env bash
# Capture the console's Available balance card from this branch.
#
# FIXTURE CAPTURE, deliberately and stated everywhere it is recorded. Both real
# balance surfaces sit behind a validated Supabase session, and there was none
# this run could legitimately use: the shared pool was saturated and the only
# self-hosted stack on this machine belongs to another agent's session. So the
# real component is rendered inside the real Next.js app with fixture props,
# through a route that exists only for the duration of this script.
#
# The route is copied in, built, captured and deleted. It is never committed:
# an unauthenticated page that renders balances has no business in the console.
set -euo pipefail

REPO=$(git rev-parse --show-toplevel)
HERE=$(cd -- "$(dirname -- "$0")" && pwd)
OUT=${OUT_DIR:-$HERE/out}
IMAGE=hive-web-console:d045proof
NET=hive-d045-console-proof
ROUTE_DIR="$REPO/apps/web-console/app/proof-fixture"

mkdir -p "$OUT"

cleanup() {
  rm -rf "$ROUTE_DIR"
  docker rm -f console-proof >/dev/null 2>&1 || true
  docker network rm "$NET" >/dev/null 2>&1 || true
}
trap cleanup EXIT
cleanup

mkdir -p "$ROUTE_DIR"
cp "$HERE/fixture-page.tsx.txt" "$ROUTE_DIR/page.tsx"

docker build -f "$REPO/deploy/docker/Dockerfile.web-console" -t "$IMAGE" "$REPO" >/dev/null

docker network create "$NET" >/dev/null

# next dev, per Dockerfile.web-console's CMD, so the served code is this
# branch's source rather than a cached bundle. The Supabase variables are
# placeholders: this route touches neither Supabase nor the control plane, and
# a real value here would be a credential in a proof script.
docker run -d --name console-proof --network "$NET" \
  -e NEXT_PUBLIC_SUPABASE_URL=https://proof.invalid \
  -e NEXT_PUBLIC_SUPABASE_ANON_KEY=proof-anon-key-placeholder \
  -e CONTROL_PLANE_BASE_URL=http://control-plane.invalid:8081 \
  "$IMAGE" >/dev/null

echo "waiting for next dev"
for _ in $(seq 1 120); do
  if docker run --rm --network "$NET" curlimages/curl:8.10.1 \
      -s -o /dev/null -w '%{http_code}' http://console-proof:3000/proof-fixture 2>/dev/null | grep -q 200; then
    echo "  up"
    break
  fi
  sleep 3
done

docker run --rm --network "$NET" \
  -v "$HERE:/work:ro" \
  -v "$OUT:/out" \
  -e OUT_DIR=/out \
  -e CONSOLE_URL=http://console-proof:3000 \
  -w /work \
  mcr.microsoft.com/playwright:v1.51.1-noble \
  bash -lc 'mkdir -p /tmp/cap && cd /tmp/cap && npm i --silent playwright@1.51.1 >/dev/null 2>&1 && cp /work/capture-console.mjs . && node capture-console.mjs'

echo "artifacts in $OUT"
ls -la "$OUT"

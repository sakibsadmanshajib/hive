#!/usr/bin/env bash
# A/B the real Caddyfile.owui against a stub upstream, before and after the
# issue #540 change. Adapted from
# docs/proof/chat-origin-admin-lockdown-2026-08-23/caddy-ab.sh, same mechanism:
# the file under test is bind-mounted byte for byte and one stub container
# answers on every service name it proxies to, so what is measured is the
# shipped matcher set rather than a paraphrase of it.
#
# usage: caddy-ab.sh <label> <path-to-Caddyfile.owui> <out-file> [--keep]
set -euo pipefail

label=$1
caddyfile=$(realpath "$2")
out=$3
keep=${4:-}
net=ab540-$label
port=3078
sp=$(mktemp -d)

cleanup() {
  docker rm -f "ab540-$label-proxy" "ab540-$label-stub" >/dev/null 2>&1 || true
  docker network rm "$net" >/dev/null 2>&1 || true
  rm -rf "$sp"
}
trap 'if [ "$keep" != "--keep" ]; then cleanup; fi' EXIT
docker rm -f "ab540-$label-proxy" "ab540-$label-stub" >/dev/null 2>&1 || true
docker network rm "$net" >/dev/null 2>&1 || true

cat > "$sp/stub.Caddyfile" <<'EOF'
{
	admin off
}
:8080, :3000 {
	respond "STUB-UPSTREAM-200" 200
}
EOF

docker network create "$net" >/dev/null
docker run -d --name "ab540-$label-stub" --network "$net" \
  --network-alias open-webui --network-alias agent-console --network-alias edge-api \
  -v "$sp/stub.Caddyfile:/etc/caddy/Caddyfile:ro" \
  caddy:2-alpine >/dev/null
docker run -d --name "ab540-$label-proxy" --network "$net" \
  -p "$port:80" \
  -v "$caddyfile:/etc/caddy/Caddyfile:ro" \
  caddy:2-alpine >/dev/null

for _ in $(seq 1 30); do
  if curl -s -o /dev/null "http://127.0.0.1:$port/" 2>/dev/null; then break; fi
  sleep 1
done

probe() {
  local path=$1 code
  code=$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$port$path")
  printf '%-56s %s\n' "$path" "$code" >> "$out"
}

: > "$out"
{
  echo "# Caddyfile.owui matcher probe for issue #540, label=$label"
  echo "# file under test: sha256=$(sha256sum "$caddyfile" | cut -c1-16)"
  echo "# upstream: one stub answering 200 to everything, reached through this"
  echo "# file's own reverse_proxy lines via docker network aliases."
  echo "# 404 = refused by Caddy. 200 = reached the (stubbed) backend."
  echo
  echo "## the second front door"
} >> "$out"
probe /agent-workspace
probe /agent-workspace/
probe /agent-workspace/tasks
probe /agent-workspace/auth/sign-in
probe //agent-workspace
probe /Agent-Workspace/tasks

echo >> "$out"
echo "## the chat shell and the native agent surface, which must keep working" >> "$out"
probe /
probe /agents
probe /api/config
probe /api/v1/hive/agent/tasks
probe /api/v1/hive/credits/balance
probe /v1/agent/tasks
probe /v1/featuregate
probe /_app/immutable/nodes/7.js
probe /agent-workspaces

echo >> "$out"
echo "# caddy container log (config load):" >> "$out"
docker logs "ab540-$label-proxy" 2>&1 | tail -2 >> "$out"
cat "$out"

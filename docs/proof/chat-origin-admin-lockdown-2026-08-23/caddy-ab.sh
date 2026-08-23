#!/usr/bin/env bash
# A/B the real Caddyfile.owui matchers against a stub upstream.
#
# The file under test is bind-mounted byte for byte from the repo, not edited:
# the upstream is replaced by giving one stub container every service name the
# Caddyfile proxies to as a network alias, so every reverse_proxy in the file
# resolves to a 200 responder. What is measured is therefore the shipped
# matcher set, not a paraphrase of it.
#
# usage: caddy-ab.sh <label> <path-to-Caddyfile.owui> <out-file>
set -euo pipefail

label=$1
caddyfile=$(realpath "$2")
out=$3
net=caddyab-$label
port=3077
sp=$(cd -- "$(dirname -- "$0")" && pwd)

cleanup() {
  docker rm -f "caddyab-$label-proxy" "caddyab-$label-stub" >/dev/null 2>&1 || true
  docker network rm "$net" >/dev/null 2>&1 || true
}
trap cleanup EXIT
cleanup

cat > "$sp/stub.Caddyfile" <<'EOF'
{
	admin off
}
:8080, :3000 {
	respond "STUB-UPSTREAM-200" 200
}
EOF

docker network create "$net" >/dev/null
docker run -d --name "caddyab-$label-stub" --network "$net" \
  --network-alias open-webui --network-alias agent-console --network-alias edge-api \
  -v "$sp/stub.Caddyfile:/etc/caddy/Caddyfile:ro" \
  caddy:2-alpine >/dev/null
docker run -d --name "caddyab-$label-proxy" --network "$net" \
  -p "$port:80" \
  -v "$caddyfile:/etc/caddy/Caddyfile:ro" \
  caddy:2-alpine >/dev/null

for _ in $(seq 1 30); do
  if curl -fsS -o /dev/null "http://127.0.0.1:$port/health" 2>/dev/null; then break; fi
  sleep 1
done

probe() {
  local method=$1 path=$2
  local code
  code=$(curl -s -o /dev/null -w '%{http_code}' -X "$method" \
    -H 'Content-Type: application/json' \
    ${3:+-d "$3"} \
    "http://127.0.0.1:$port$path")
  printf '%-6s %-56s %s\n' "$method" "$path" "$code" >> "$out"
}

: > "$out"
{
  echo "# Caddyfile.owui matcher probe, label=$label"
  echo "# file under test: $(basename "$caddyfile") sha256=$(sha256sum "$caddyfile" | cut -c1-16)"
  echo "# upstream: stub answering 200 to everything, reached through the"
  echo "# Caddyfile's own reverse_proxy lines via docker network aliases."
  echo "# 404 = refused by Caddy. 200 = reached the (stubbed) chat backend."
  echo
  echo "## Credential-bearing reads that must be refused (#769, #948)"
} >> "$out"
probe GET /api/v1/configs/export
probe GET /api/v1/configs/import
probe GET /api/v1/configs/namespace/oauth
probe GET /api/v1/configs/namespace/rag
probe GET /openai/config
probe GET /admin/settings

echo >> "$out"
echo "## Admin writes that must be refused (#736, #948, #949)" >> "$out"
probe POST /api/v1/functions/create '{}'
probe POST /api/v1/functions/load/url '{}'
probe POST /api/v1/functions/id/hive_jwt_forward/update '{}'
probe POST /api/v1/functions/id/hive_jwt_forward/toggle
probe POST /api/v1/functions/id/hive_jwt_forward/toggle/global
probe DELETE /api/v1/functions/id/hive_jwt_forward/delete
probe POST /api/v1/configs/import '{}'
probe POST /api/v1/configs/code_execution '{}'
probe POST /api/v1/configs/connections '{}'
probe POST /api/v1/configs/tool_servers '{}'

echo >> "$out"
echo "## The chat product, which must keep working" >> "$out"
probe GET /
probe GET /api/config
probe GET /api/v1/auths/
probe POST /api/v1/auths/signin '{}'
probe GET /api/v1/configs/banners
probe GET /api/v1/configs/models
probe GET /api/v1/models/list
probe GET /openai/models
probe POST /openai/chat/completions '{}'
probe GET /api/v1/knowledge/
probe POST /api/v1/users/user/settings/update '{}'
probe POST /api/v1/users/user/info/update '{}'
probe POST /api/v1/models/model/toggle '{}'
probe POST /api/v1/functions/id/hive_jwt_forward/valves/user/update '{}'
probe GET /api/v1/functions/id/hive_jwt_forward
probe POST /api/v1/users/6fcba712-5631-4e7f-9c9c-ce41047914fa/update '{}'
probe GET /v1/featuregate
probe GET /agent-workspace

echo >> "$out"
echo "# caddy container log (config load):" >> "$out"
docker logs "caddyab-$label-proxy" 2>&1 | tail -3 >> "$out"
cat "$out"

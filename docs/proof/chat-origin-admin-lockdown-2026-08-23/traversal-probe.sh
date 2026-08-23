#!/usr/bin/env bash
# Adversarial probe: can a normalisation trick reach a blocked path?
#
# Every request is sent with `curl --path-as-is`, so curl does not collapse the
# dot segments or the doubled slashes on the client side the way a browser
# would. Two questions, and they need different answers to matter:
#   1. does Caddy's matcher refuse it (404 from Caddy), and
#   2. if it does not, does anything downstream normalise it back onto the
#      blocked route?
# The stub upstream answers 200 to everything, so a 200 here means only "Caddy
# forwarded it"; the second question is answered against a real Open WebUI in
# live-proof.log.
set -uo pipefail

label=$1
caddyfile=$(realpath "$2")
out=$3
net=trav-$label
port=3078
sp=$(mktemp -d)
cat > "$sp/stub.Caddyfile" <<'EOF'
{
	admin off
}
:8080, :3000 {
	respond "STUB-UPSTREAM-200" 200
}
EOF

cleanup() {
  docker rm -f "trav-$label-proxy" "trav-$label-stub" >/dev/null 2>&1 || true
  docker network rm "$net" >/dev/null 2>&1 || true
  rm -rf "$sp"
}
trap cleanup EXIT
cleanup

docker network create "$net" >/dev/null
docker run -d --name "trav-$label-stub" --network "$net" \
  --network-alias open-webui --network-alias agent-console --network-alias edge-api \
  -v "$sp/stub.Caddyfile:/etc/caddy/Caddyfile:ro" caddy:2-alpine >/dev/null
docker run -d --name "trav-$label-proxy" --network "$net" -p "$port:80" \
  -v "$caddyfile:/etc/caddy/Caddyfile:ro" caddy:2-alpine >/dev/null
sleep 4

: > "$out"
probe() {
  local method=$1 path=$2 code
  code=$(curl -s --path-as-is -o /dev/null -w '%{http_code}' -X "$method" \
    -H 'Content-Type: application/json' -d '{}' "http://127.0.0.1:$port$path")
  printf '%-6s %-62s %s\n' "$method" "$path" "$code" | tee -a "$out"
}

echo "# traversal and normalisation probes, --path-as-is, label=$label" | tee -a "$out"
echo "# 404 = refused by Caddy. 200 = forwarded to the (stub) upstream." | tee -a "$out"
probe POST "//api/v1/functions/create"
probe POST "///api/v1/functions/create"
probe POST "/api/v1//functions/create"
probe POST "/api/v1/functions//create"
probe POST "/API/v1/FUNCTIONS/Create"
probe POST "/api/v99/functions/create"
probe POST "/api/v1/functions/id/../create"
probe POST "/api/v1/hive/../functions/create"
probe POST "/api/v1/hive/../configs/import"
probe POST "/api//v1/configs/import"
probe GET  "/api/v1/configs/namespace/../namespace/oauth"
probe GET  "/api/v1/./configs/namespace/oauth"
probe POST "/api/v1/functions/id/x/valves/user/update"
probe POST "/api/v1/functions/id/../valves/user/update"
echo | tee -a "$out"
echo "# percent-encoded separators (Go decodes %2F into URL.Path)" | tee -a "$out"
probe POST "/api/v1/functions%2Fcreate"
probe POST "/api/v1%2Fconfigs%2Fimport"

#!/usr/bin/env bash
# Transient-outage proof. A request arrives while the `open-webui` name does
# not resolve at all, the upstream appears 8s later, and the held request is
# served instead of 502ing. Run against both Caddyfile variants.
set -u
VARIANT="$1"
PORT="$2"
docker rm -f open-webui >/dev/null 2>&1 || true
echo "=== $VARIANT: upstream absent at request time, appears after 8s ==="
curl -s -o /dev/null -w "code=%{http_code} time=%{time_total}\n" "http://127.0.0.1:$PORT/" > /tmp/caddy502/result.$VARIANT &
CURL_PID=$!
sleep 8
docker run -d --name open-webui --network caddy502test \
  -v /tmp/caddy502/srv:/srv \
  caddy:2-alpine@sha256:86deaf5e3d3408a6ccec08fbb79989783dd26e206ae10bcf78a801dc8c9ab794 \
  caddy file-server --listen :8080 --root /srv >/dev/null
wait $CURL_PID
cat /tmp/caddy502/result.$VARIANT
docker rm -f open-webui >/dev/null 2>&1 || true

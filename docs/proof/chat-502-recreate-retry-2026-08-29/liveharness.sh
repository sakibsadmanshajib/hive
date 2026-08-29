#!/usr/bin/env bash
# Harness Caddy running the PATCHED Caddyfile.owui on the live compose network,
# fronting the real open-webui container. Read-only: it mounts nothing the real
# stack shares and binds to loopback only.
set -u
IMG=caddy:2-alpine@sha256:86deaf5e3d3408a6ccec08fbb79989783dd26e206ae10bcf78a801dc8c9ab794
docker rm -f caddy502-live >/dev/null 2>&1 || true
docker run -d --name caddy502-live --network hive_default \
  -p 127.0.0.1:18097:80 \
  -e HIVE_CHAT_EXTERNAL_SCHEME=http \
  -v /tmp/caddy502/Caddyfile.post:/etc/caddy/Caddyfile:ro \
  "$IMG" >/dev/null
sleep 3
echo "--- live harness status ---"
docker ps --filter name=caddy502-live --format '{{.Status}}'
echo "--- real chat upstream through the patched config ---"
curl -s -o /dev/null -w "code=%{http_code} time=%{time_total}\n" http://127.0.0.1:18097/
echo "--- keep the throwaway-network pair up and upstream-less for the A/B shots ---"
docker rm -f open-webui >/dev/null 2>&1 || true
docker ps --filter name=caddy502- --format '{{.Names}} {{.Status}}'

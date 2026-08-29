#!/usr/bin/env bash
# A/B the Caddyfile against an upstream name that does not resolve, on a
# throwaway user-defined network whose embedded DNS SERVFAILs unknown names --
# the exact failure the box logs as "server misbehaving".
set -u
VARIANT="$1"        # pre | post
PORT="$2"
NAME="caddy502-$VARIANT"
docker network create caddy502test >/dev/null 2>&1 || true
docker rm -f "$NAME" >/dev/null 2>&1 || true
docker run -d --name "$NAME" --network caddy502test \
  -p "127.0.0.1:$PORT:80" \
  -v "/tmp/caddy502/Caddyfile.$VARIANT:/etc/caddy/Caddyfile:ro" \
  caddy:2-alpine@sha256:86deaf5e3d3408a6ccec08fbb79989783dd26e206ae10bcf78a801dc8c9ab794 >/dev/null
sleep 3
echo "--- $VARIANT container status ---"
docker ps --filter "name=$NAME" --format '{{.Status}}'
echo "--- $VARIANT: 5 requests to / ---"
for i in 1 2 3 4 5; do
  curl -s -o /dev/null -w "code=%{http_code} time=%{time_total}\n" "http://127.0.0.1:$PORT/"
done
echo "--- $VARIANT caddy error log (last 2) ---"
docker logs "$NAME" 2>&1 | grep -o '"msg":"[^"]*"' | tail -2

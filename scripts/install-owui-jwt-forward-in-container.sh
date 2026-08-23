#!/usr/bin/env bash
# Install the hive_jwt_forward Open WebUI Function without sending the call
# through the public chat origin (#736).
#
# scripts/install-owui-jwt-forward.py is still the single implementation of the
# install. This wrapper only changes where it runs from: it copies the script
# and the Function body into the running chat container and executes them there
# against Open WebUI's own loopback address, so the Functions API is reached
# inside the container's network namespace instead of through caddy-owui's
# published port.
#
# Why that matters. Every endpoint the installer calls
# (`/api/v1/functions/create`, `/id/<id>/update`, `/id/<id>/toggle`,
# `/id/<id>/toggle/global`) executes or enables Python inside a container
# holding PGVECTOR_DB_URL and OWUI_SHIM_KEY. Caddyfile.owui intends to drop
# admin mutation verbs on the public origin and, until this script existed,
# could not: `path /api/v*/functions*` falls through to Go's path.Match, whose
# `*` never crosses a slash, so it matched `/api/v1/functions` and no child of
# it. The deploy depended on that gap, which is why the block could not simply
# be tightened -- reroute first, then block. Both halves land together; see
# the @adminMutationSubtree matcher in deploy/docker/Caddyfile.owui.
#
# Environment:
#   OWUI_ADMIN_TOKEN      required, an Open WebUI admin session bearer token
#                         (scripts/owui-mint-admin-token.py mints a five minute
#                         one inside the container without touching a password).
#   HIVE_COMPOSE_FLAGS    the caller's compose flags, exactly as the deploy
#                         workflow spells them. Defaults to the invocation
#                         README.md documents for a plain local stack.
#   HIVE_OWUI_SERVICE     compose service name, default open-webui.
#   HIVE_OWUI_PORT        the port Open WebUI listens on inside the container,
#                         default 8080 (what Caddyfile.owui proxies to).
#
# The token never leaves the container's loopback interface here, so the
# installer's require_safe_origin guard is satisfied without relaxing it: it
# permits plaintext only for a loopback host, and 127.0.0.1 inside the
# container namespace is exactly that.
set -euo pipefail

if [ -z "${OWUI_ADMIN_TOKEN:-}" ]; then
  echo "OWUI_ADMIN_TOKEN is empty. Mint one on a deployment box with" \
    "scripts/owui-mint-admin-token.py." >&2
  exit 2
fi

repo_root=$(cd -- "$(dirname -- "$0")/.." && pwd)
service=${HIVE_OWUI_SERVICE:-open-webui}
port=${HIVE_OWUI_PORT:-8080}
installer_in_container=/tmp/hive-install-owui-jwt-forward.py
source_in_container=/tmp/hive-jwt-forward-source.py

cd "$repo_root/deploy/docker"

# Word splitting is deliberate: HIVE_COMPOSE_FLAGS carries several flags in one
# variable, the same way every `docker compose $HIVE_COMPOSE_FLAGS` line in
# .github/workflows/deploy-demo-box.yml consumes it.
# shellcheck disable=SC2206,SC2086
compose=(docker compose ${HIVE_COMPOSE_FLAGS:---env-file ../../.env})

cleanup() {
  "${compose[@]}" exec -T "$service" \
    rm -f "$installer_in_container" "$source_in_container" >/dev/null 2>&1 || true
}
trap cleanup EXIT

"${compose[@]}" cp \
  "$repo_root/scripts/install-owui-jwt-forward.py" \
  "$service:$installer_in_container"
"${compose[@]}" cp \
  "$repo_root/deploy/docker/pipelines/hive_jwt_forward.py" \
  "$service:$source_in_container"

"${compose[@]}" exec -T -e OWUI_ADMIN_TOKEN "$service" \
  python3 "$installer_in_container" \
  --base-url "http://127.0.0.1:$port" \
  --source "$source_in_container"

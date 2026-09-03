#!/usr/bin/env bash
# Run scripts/owui-promote-instance-admin.py inside the running chat container.
#
# Same shape as install-owui-jwt-forward-in-container.sh, and for the same
# reason: the work has to happen in the container's own namespace, against the
# database the server actually has open. This wrapper only decides where the
# script runs.
#
# Why this exists at all is in the python script's own docstring: issue #748
# removed every self-appearing Open WebUI admin, and a throwaway test stack
# needs one for the Function install without being handed the platform-wide
# authority a real grant carries.
#
# Environment:
#   OWUI_PROMOTE_EMAIL    required, the address to promote. It must already have
#                         signed in once, because the script creates nothing.
#   OWUI_PROMOTE_ROLE     optional, defaults to admin. Set it to `user` to UNDO
#                         a promotion, which is what the chat visual proof's
#                         projects scenario does once the install is finished
#                         (issue #1505): the capture reuses the promoted session
#                         and its whole claim is that an ORDINARY account can do
#                         the thing.
#   HIVE_COMPOSE_FLAGS    the caller's compose flags, exactly as the deploy
#                         workflow spells them. Defaults to the invocation
#                         README.md documents for a plain local stack.
#   HIVE_OWUI_SERVICE     compose service name, default open-webui.
set -euo pipefail

if [ -z "${OWUI_PROMOTE_EMAIL:-}" ]; then
  echo "OWUI_PROMOTE_EMAIL is empty; nothing to promote." >&2
  exit 2
fi

repo_root=$(cd -- "$(dirname -- "$0")/.." && pwd)
service=${HIVE_OWUI_SERVICE:-open-webui}

cd "$repo_root/deploy/docker"

# Word splitting is deliberate: HIVE_COMPOSE_FLAGS carries several flags in one
# variable, the same way every `docker compose $HIVE_COMPOSE_FLAGS` line in
# .github/workflows/deploy-demo-box.yml consumes it.
# shellcheck disable=SC2206,SC2086
compose=(docker compose ${HIVE_COMPOSE_FLAGS:---env-file ../../.env})

"${compose[@]}" exec -T -e OWUI_PROMOTE_EMAIL -e OWUI_PROMOTE_ROLE "$service" python3 - \
  < "$repo_root/scripts/owui-promote-instance-admin.py"

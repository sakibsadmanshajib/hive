#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT_DIR"

echo "==> Starting or verifying local Supabase stack"
npx supabase start

echo "==> Reading local Supabase environment"
set -a
# shellcheck disable=SC1090
source <(npx supabase status -o env)
set +a

if [[ -z "${API_URL:-}" || -z "${ANON_KEY:-}" || -z "${SERVICE_ROLE_KEY:-}" ]]; then
  echo "Missing required Supabase values from 'npx supabase status -o env'" >&2
  exit 1
fi

export SUPABASE_URL="${API_URL}"
export NEXT_PUBLIC_SUPABASE_URL="${API_URL}"
export SUPABASE_SERVICE_ROLE_KEY="${SERVICE_ROLE_KEY}"
export NEXT_PUBLIC_SUPABASE_ANON_KEY="${ANON_KEY}"
export NEXT_PUBLIC_API_BASE_URL="${NEXT_PUBLIC_API_BASE_URL:-http://127.0.0.1:8080}"
export OLLAMA_FREE_MODEL="${OLLAMA_FREE_MODEL:-${OLLAMA_MODEL:-llama3.1:8b}}"

echo "==> Using local Supabase API: ${SUPABASE_URL}"
echo "==> Starting Hive dev stack with hot reload"

exec docker compose \
  -f docker-compose.yml \
  -f docker-compose.dev.yml \
  up --build "$@"

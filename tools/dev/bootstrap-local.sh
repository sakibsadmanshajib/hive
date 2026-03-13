#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT_DIR"

echo "==> Starting or verifying local Supabase stack"
npx supabase start

echo "==> Resetting local Supabase database and applying migrations"
npx supabase db reset --yes

OLLAMA_MODEL="${OLLAMA_MODEL:-llama3.1:8b}"

echo "==> Starting local Ollama service for default model bootstrap"
docker compose up -d ollama

echo "==> Pulling default Ollama model: ${OLLAMA_MODEL}"
docker compose exec ollama ollama pull "${OLLAMA_MODEL}"

echo "==> Local bootstrap complete"
echo "Run 'pnpm stack:dev' for daily development startup."

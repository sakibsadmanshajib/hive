#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
UPSTREAM_DIR="$SCRIPT_DIR/../upstream"
SPEC_URL="https://raw.githubusercontent.com/openai/openai-openapi/refs/heads/manual_spec/openapi.yaml"
SOURCE_REPO="https://github.com/openai/openai-openapi"
SOURCE_BRANCH="manual_spec"

mkdir -p "$UPSTREAM_DIR"

echo "Downloading OpenAI OpenAPI spec..."
curl -fsSL "$SPEC_URL" -o "$UPSTREAM_DIR/openapi.yaml"

cat > "$UPSTREAM_DIR/SPEC_VERSION" <<EOF
source: $SOURCE_REPO
branch: $SOURCE_BRANCH
downloaded: $(date -u +%Y-%m-%d)
EOF

echo "Spec downloaded to $UPSTREAM_DIR/openapi.yaml"
echo "Version info written to $UPSTREAM_DIR/SPEC_VERSION"

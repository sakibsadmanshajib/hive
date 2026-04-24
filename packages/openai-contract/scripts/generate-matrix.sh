#!/bin/sh
set -eu

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"
MATRIX_PATH="$REPO_ROOT/packages/openai-contract/matrix/support-matrix.json"
UPSTREAM_SPEC_PATH="$REPO_ROOT/packages/openai-contract/upstream/openapi.yaml"
GENERATED_SPEC_PATH="$REPO_ROOT/packages/openai-contract/generated/hive-openapi.yaml"
MARKDOWN_PATH="$REPO_ROOT/docs/support-matrix.md"

if [ ! -f "$MATRIX_PATH" ]; then
	echo "ERROR: Matrix file not found at $MATRIX_PATH"
	exit 1
fi

if [ ! -f "$UPSTREAM_SPEC_PATH" ]; then
	echo "ERROR: Upstream OpenAPI spec not found at $UPSTREAM_SPEC_PATH"
	exit 1
fi

python3 "$SCRIPT_DIR/sync_hive_contract.py"

if grep -q "https://api.openai.com/v1" "$GENERATED_SPEC_PATH"; then
	echo "ERROR: Generated spec still contains the upstream OpenAI server URL"
	exit 1
fi

if ! grep -q "x-hive-status:" "$GENERATED_SPEC_PATH"; then
	echo "ERROR: Generated spec is missing x-hive-status annotations"
	exit 1
fi

PUBLIC_OPERATIONS="$(grep -c "x-hive-status:" "$GENERATED_SPEC_PATH")"

echo "Generated $PUBLIC_OPERATIONS public operations"
echo "Wrote $GENERATED_SPEC_PATH"
echo "Wrote $MARKDOWN_PATH"

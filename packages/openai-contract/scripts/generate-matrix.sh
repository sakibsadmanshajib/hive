#!/usr/bin/env bash
set -euo pipefail

# generate-matrix.sh
#
# Regenerates support-matrix.json from the overlay classifications.
# Currently the matrix is hand-authored from the spec and overlay.
# This script serves as a reminder and placeholder for future automation.
#
# To regenerate, run the Python script used during initial creation
# or manually update packages/openai-contract/matrix/support-matrix.json

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MATRIX_PATH="$SCRIPT_DIR/../matrix/support-matrix.json"

if [ -f "$MATRIX_PATH" ]; then
    ENDPOINT_COUNT=$(python3 -c "import json; print(len(json.load(open('$MATRIX_PATH'))['endpoints']))")
    echo "Current matrix: $MATRIX_PATH ($ENDPOINT_COUNT endpoints)"
    echo "To regenerate, update the overlay and re-run the generation script."
else
    echo "ERROR: Matrix file not found at $MATRIX_PATH"
    echo "Run import-spec.sh first, then create the matrix."
    exit 1
fi

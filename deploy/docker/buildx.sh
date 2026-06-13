#!/usr/bin/env bash
# deploy/docker/buildx.sh
#
# Build (and optionally push) the Hive Go service images for both
# linux/amd64 and linux/arm64 using docker buildx.
#
# Usage:
#   ./deploy/docker/buildx.sh [--push] [--tag TAG] [--registry REGISTRY]
#
# Environment variables (all have defaults):
#   HIVE_REGISTRY   Container registry prefix  (default: ghcr.io/sakibsadmanshajib/hive)
#   HIVE_TAG        Image tag                  (default: git short SHA, or "latest" if no git)
#   HIVE_PUSH       Set to "1" to push after building (default: 0)
#   HIVE_PLATFORMS  Comma-separated platforms  (default: linux/amd64,linux/arm64)
#   HIVE_BUILDER    Buildx builder name        (default: hive-multiarch)
#
# Flag equivalents (flags override env vars):
#   --push              Push to registry after build
#   --tag TAG           Override HIVE_TAG
#   --registry REGISTRY Override HIVE_REGISTRY
#
# The script must be run from the repo root (where go.work lives) because
# Docker build context is "." and Dockerfiles use paths like apps/edge-api/.

set -euo pipefail

# ---- defaults ---------------------------------------------------------------
REGISTRY="${HIVE_REGISTRY:-ghcr.io/sakibsadmanshajib/hive}"
TAG="${HIVE_TAG:-$(git rev-parse --short HEAD 2>/dev/null || echo "latest")}"
PUSH="${HIVE_PUSH:-0}"
PLATFORMS="${HIVE_PLATFORMS:-linux/amd64,linux/arm64}"
BUILDER="${HIVE_BUILDER:-hive-multiarch}"

# ---- flag parsing -----------------------------------------------------------
while [[ $# -gt 0 ]]; do
  case "$1" in
    --push)
      PUSH=1
      shift
      ;;
    --tag)
      TAG="$2"
      shift 2
      ;;
    --registry)
      REGISTRY="$2"
      shift 2
      ;;
    *)
      echo "Unknown flag: $1" >&2
      echo "Usage: $0 [--push] [--tag TAG] [--registry REGISTRY]" >&2
      exit 1
      ;;
  esac
done

EDGE_API_IMAGE="${REGISTRY}/edge-api:${TAG}"
CONTROL_PLANE_IMAGE="${REGISTRY}/control-plane:${TAG}"

echo "==> Hive multi-arch build"
echo "    Platforms : ${PLATFORMS}"
echo "    Registry  : ${REGISTRY}"
echo "    Tag       : ${TAG}"
echo "    Push      : ${PUSH}"
echo "    Builder   : ${BUILDER}"
echo

# ---- ensure buildx builder exists -------------------------------------------
if ! docker buildx inspect "${BUILDER}" &>/dev/null; then
  echo "==> Creating buildx builder: ${BUILDER}"
  docker buildx create --name "${BUILDER}" --driver docker-container --bootstrap
fi

docker buildx use "${BUILDER}"

# ---- build flags ------------------------------------------------------------
BUILD_FLAGS=(
  --platform "${PLATFORMS}"
  --provenance=false   # avoids extra index layers for registries that do not support them
)

if [[ "${PUSH}" == "1" ]]; then
  BUILD_FLAGS+=(--push)
else
  # Without --push, load into local docker daemon. However, multi-platform
  # images cannot be loaded simultaneously; load one platform at a time or
  # omit --load and use --output type=oci,dest=<file> for artifact builds.
  # Here we default to --load when push is disabled, which loads the image
  # for the BUILD host's native arch only (buildx limitation).
  echo "WARNING: --load with multi-platform only loads the host-native arch." \
       "Use --push or export with --output to get a true multi-arch manifest." >&2
  BUILD_FLAGS+=(--load)
fi

# ---- edge-api ---------------------------------------------------------------
echo "==> Building edge-api -> ${EDGE_API_IMAGE}"
docker buildx build \
  "${BUILD_FLAGS[@]}" \
  --file deploy/docker/Dockerfile.edge-api.prod \
  --tag "${EDGE_API_IMAGE}" \
  .

echo "    Done: ${EDGE_API_IMAGE}"
echo

# ---- control-plane ----------------------------------------------------------
echo "==> Building control-plane -> ${CONTROL_PLANE_IMAGE}"
docker buildx build \
  "${BUILD_FLAGS[@]}" \
  --file deploy/docker/Dockerfile.control-plane.prod \
  --tag "${CONTROL_PLANE_IMAGE}" \
  .

echo "    Done: ${CONTROL_PLANE_IMAGE}"
echo

echo "==> All builds complete."
if [[ "${PUSH}" == "1" ]]; then
  echo "    Images pushed to ${REGISTRY} with tag ${TAG}."
else
  echo "    Images NOT pushed. Re-run with --push or HIVE_PUSH=1 to publish."
fi

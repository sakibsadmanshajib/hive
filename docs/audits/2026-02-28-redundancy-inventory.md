# Redundancy Inventory (2026-02-28)

## Removed

- Root legacy Python runtime tree (`app/`) duplicated TypeScript API responsibilities.
- Root Python tests (`tests/`) duplicated TypeScript test intent with stale naming/flow assumptions.
- Duplicate OpenAPI artifact (`openapi/openapi.yaml`) diverged from package-level contract and introduced header drift.

## Kept intentionally

- `packages/openapi/openapi.yaml` remains canonical API contract artifact.
- `docs/plans/**` historical plan artifacts remain for traceability.

## Follow-up

- Tighten OpenAPI parity against current implemented endpoints/headers in a dedicated contract alignment pass.

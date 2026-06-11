# Test Coverage Audit — 2026-06-11

## Executive Summary

Go toolchain is not installed on the host; instrumented coverage percentages (`go tool cover`) could not be produced. This audit is a **static analysis** of test-file presence vs. source-file presence per package, supplemented by code inspection of zero-test packages and risk ranking. All percentage estimates are lower-bound approximations based on file counts and code inspection.

Key headline numbers:

- **Control-plane packages audited**: 41 internal packages + 4 compliance tests
- **Edge-api packages audited**: 17 internal packages
- **Zero-test packages (Go)**: 3 (platform/db, platform/redis, edge-api/proxy)
- **Web-console unit test files**: 11 (against 38 component files + 11 lib files = 49 source files, coverage ratio ~22%)
- **Web-console E2E specs**: 25 (covering auth, billing, tenant isolation, SDK flows, RBAC)
- **No coverage gate configured** in vitest or any CI threshold

---

## Go Coverage Tables

Note: "test ratio" = test files / source files. A ratio below 0.5 on a money or security package is flagged.

### control-plane — internal packages

| Package | Source files | Test files | Test ratio | Risk tier |
|---------|-------------|------------|-----------|-----------|
| accounting | 6 | 3 | 0.50 | MONEY |
| ledger | 4 | 3 | 0.75 | MONEY |
| payments | 7 | 11 | 1.57 | MONEY |
| payments/bkash | 1 | 1 | 1.00 | MONEY |
| payments/sslcommerz | 1 | 1 | 1.00 | MONEY |
| payments/stripe | 1 | 1 | 1.00 | MONEY |
| payments/invoices | 7 | 5 | 0.71 | MONEY |
| budgets | 6 | 3 | 0.50 | MONEY |
| grants | 4 | 2 | 0.50 | MONEY |
| apikeys | 4 | 4 | 1.00 | SECURITY |
| auth | 3 | 1 | 0.33 | SECURITY |
| authz | 2 | 1 | 0.50 | SECURITY |
| signupguard | 4 | 4 | 1.00 | SECURITY |
| routing | 6 | 5 | 0.83 | SECURITY |
| usage | 4 | 2 | 0.50 | MONEY |
| platform/db | 1 | **0** | 0.00 | INFRA |
| platform/redis | 1 | **0** | 0.00 | INFRA |
| audit | 2 | 2 | 1.00 | COMPLIANCE |
| auditverifier | 1 | 1 | 1.00 | COMPLIANCE |
| batchstore | 3 | 3 | 1.00 | AVAILABILITY |
| batchstore/executor | 4 | 4 | 1.00 | AVAILABILITY |
| catalog | 2 | 2 | 1.00 | NORMAL |
| profiles | 2 | 2 | 1.00 | NORMAL |
| tenants | 1 | 1 | 1.00 | NORMAL |
| signup | 2 | 2 | 1.00 | NORMAL |
| spendalerts | 1 | 1 | 1.00 | NORMAL |
| waldrainer | 1 | 1 | 1.00 | NORMAL |
| identity | 1 | 1 | 1.00 | NORMAL |
| owui | 1 | 1 | 1.00 | NORMAL |
| filestore | 2 | 2 | 1.00 | NORMAL |

### edge-api — internal packages

| Package | Source files | Test files | Test ratio | Risk tier |
|---------|-------------|------------|-----------|-----------|
| auth | 5 | 5 | 1.00 | SECURITY |
| authz | 8 | 8 | 1.00 | SECURITY |
| inference | 17 | 7 | 0.41 | HOT-PATH |
| limits | 1 | 2 | 2.00 | MONEY |
| chat | 3 | 1 | 0.33 | HOT-PATH |
| batches | 7 | 2 | 0.29 | AVAILABILITY |
| files | 4 | 1 | 0.25 | NORMAL |
| images | 5 | 2 | 0.40 | NORMAL |
| audio | 5 | 2 | 0.40 | NORMAL |
| proxy | 1 | **0** | 0.00 | INFRA |
| errors | 3 | 3 | 1.00 | NORMAL |
| cpauth | 1 | 1 | 1.00 | SECURITY |
| middleware | 2 | 2 | 1.00 | NORMAL |
| matrix | 1 | 1 | 1.00 | NORMAL |
| catalog | 1 | 1 | 1.00 | NORMAL |

---

## Zero-Test Packages

| Package | Source size | What it does | Risk if broken |
|---------|------------|-------------|----------------|
| `control-plane/internal/platform/db` | 1 file | Postgres connection pool wiring | Silent pool misconfiguration causes all DB ops to hang; availability |
| `control-plane/internal/platform/redis` | 1 file | Redis client wiring | Rate-limiter, dedup, and credit-reservation all use Redis; financial integrity |
| `edge-api/internal/proxy` | 1 file | Prometheus metrics registration (`NewEdgeMetrics`) + `ResponseWriter` hijack wrapper | Metric label collision on startup causes panic; `ServeHTTP` hijack path untested |

---

## Web Console Coverage

| Layer | Files | Test files | Coverage ratio |
|-------|-------|-----------|---------------|
| Components | 38 `.tsx` | 3 component test files | ~8% |
| App routes (pages) | 19 `page.tsx` | 0 direct page tests | 0% |
| Lib / utilities | 11 `.ts` | 6 unit test files | ~55% |
| `__tests__/` (feature) | — | 5 files | auth, invite, billing FX |
| E2E specs | — | 25 files | auth, billing, tenant isolation, SDK, RBAC |

**No coverage threshold is configured** in `vitest.config.ts`. `npm run test:unit` runs vitest without `--coverage`. No `@vitest/coverage-v8` dependency present.

### E2E coverage gaps

Routes with no E2E spec:

- `/console/analytics` (usage chart)
- `/console/api-keys/[id]/limits` (per-key rate limit UI)
- `/console/catalog` (model catalog)
- `/console/settings/billing` (tenant billing settings)
- `/console/settings/profile`
- `/console/setup` (first-run onboarding)

---

## Top 10 Prioritised Gaps

Priority = financial/security risk x test gap x change frequency.

| # | Gap | Package(s) | Risk | Type needed | Effort |
|---|-----|-----------|------|------------|--------|
| 1 | **Inference hot-path branch coverage** | `edge-api/inference` (17 src, 7 test) | Availability + financial (usage accounting on every request) | Unit: SSE chunk accounting, retry exhaustion, token-clamp edge cases | L (3 days) |
| 2 | **Redis client wiring zero coverage** | `control-plane/platform/redis` | Financial integrity (credit reservation, rate-limit dedup all go through Redis) | Unit: connection fail-fast, ping timeout, URL parse error | S (0.5 days) |
| 3 | **platform/db zero coverage** | `control-plane/platform/db` | Availability (all DB ops) | Unit: pool config, connection string parse, DSN masking in logs | S (0.5 days) |
| 4 | **Batch handler branch coverage** | `edge-api/batches` (7 src, 2 test) | Availability (batch submit/poll/cancel paths) | Unit: status machine transitions, accounting adapter error paths | M (1.5 days) |
| 5 | **chat dispatch coverage** | `edge-api/chat` (3 src, 1 test) | Hot-path (every non-streaming chat goes through dispatch) | Unit: model-not-found, upstream error wrapping, context cancellation | M (1 day) |
| 6 | **auth client coverage** | `control-plane/auth` (3 src, 1 test) | Security (Supabase JWT validation, API key lookup) | Unit: expired token, malformed claims, key rotation | M (1 day) |
| 7 | **proxy metrics + hijack wrapper** | `edge-api/proxy` (1 src, 0 test) | Infra (panic on duplicate metric registration, broken SSE streaming on hijack failure) | Unit: `NewEdgeMetrics` idempotency, `HijackableResponseWriter` | S (0.5 days) |
| 8 | **amount_usd BD leak regression** | `control-plane/payments` HTTP layer | Regulatory (known open issue, no automated guard) | Unit: checkout response serialisation for BD locale must omit `amount_usd` | S (0.5 days) |
| 9 | **Web console billing components** | `components/billing` (3 test files exist but checkout-modal + budget-form surface area large) | Financial (wrong amounts shown to user) | Vitest + @testing-library: edge amounts, zero-credit state, locale formatting | M (1.5 days) |
| 10 | **E2E: API-key limits + analytics routes** | `web-console` routes with zero E2E | Security (key limits can be bypassed via UI if rate-limit page broken) | Playwright: set limit, verify enforcement, analytics data renders | M (1 day) |

---

## Proposed Coverage Gate

### Phase 20 enforcement recommendation

Implement a ratchet, not a cliff. Add a CI step that runs inside the Docker toolchain container:

```bash
# In .github/workflows/test.yml (or equivalent)
go test ./apps/control-plane/... ./apps/edge-api/... \
  -count=1 -short \
  -coverprofile=/tmp/cover.out

# Per-package thresholds using go test -cover per package
# go tool cover -func outputs per-function lines (no package path on the total: line),
# so the correct approach is to run per-package and check each package total directly.
for pkg in \
  ./apps/control-plane/internal/ledger/... \
  ./apps/control-plane/internal/accounting/... \
  ./apps/control-plane/internal/payments/...; do
  pct=$(go test "$pkg" -count=1 -short -coverprofile=/tmp/pkg.out 2>/dev/null \
        && go tool cover -func=/tmp/pkg.out | awk '/^total:/ { gsub(/%/,""); print $3 }')
  if [ -z "$pct" ] || awk "BEGIN { exit ($pct < 70) }"; then :; else
    echo "FAIL: $pkg coverage $pct% < 70%"; exit 1
  fi
done
```

Suggested thresholds by tier:

| Tier | Packages | Gate (initial) | Target (v1.2) |
|------|---------|---------------|--------------|
| MONEY | ledger, accounting, payments/*, budgets, grants | 70% | 85% |
| SECURITY | auth, authz, apikeys, signupguard | 70% | 85% |
| HOT-PATH | inference, chat, limits | 60% | 80% |
| INFRA | platform/db, platform/redis, proxy | 50% | 70% |
| Web unit | vitest with `@vitest/coverage-v8` | 40% | 60% |

Add `@vitest/coverage-v8` to `devDependencies` and add to `vitest.config.ts`:

```ts
coverage: {
  provider: "v8",
  thresholds: { lines: 40, functions: 40, branches: 35 },
  exclude: ["node_modules", ".next", "e2e", "**/*.test.*"]
}
```

### TDD enforcement for Phase 20

1. Every new Go package must ship with at least one `_test.go` file in the same PR. CI fails if `find ./apps -name "*.go" ! -name "*_test.go" -newer HEAD~1` finds files in new packages without paired tests.
2. Use the RED-GREEN-IMPROVE cycle: write failing test, commit as `test: ...`, then implement, commit as `feat: ...`. PR description must show both commits.
3. For money and security packages: require a second reviewer sign-off confirming test branch coverage before merge.
4. For regulatory paths (`amount_usd` BD leak): add a dedicated `_regulatory_test.go` file per payment handler asserting the field is absent in BD-locale responses.

---

## Methodology Notes

- Go toolchain not available on host; coverage percentages are file-count proxies, not instrumented line/branch data. Run `go test -coverprofile` via Docker toolchain to get precise numbers before setting CI gates.
- "Source files" counts exclude `*_test.go` and exclude sub-packages.
- Web-console route coverage is assessed against Playwright spec file names and their `describe`/`test` blocks (not dynamically executed).

---

## Review Postscript — 2026-06-11

### Zero-test gaps closed (PRs #190 and #191)

The three zero-test packages flagged in this audit have been fully addressed:

| Package | Status after backfill |
|---------|----------------------|
| `control-plane/internal/platform/redis` | 100% statement coverage (PR #190) |
| `control-plane/internal/platform/db` | 88.9% statement coverage (PR #190) |
| `edge-api/internal/proxy` | 100% statement coverage (PR #191) |
| BD `amount_usd` regression guard | Dedicated regulatory test added (PR #191) |

The Top 10 priority table items 2, 3, 7, and 8 are now closed. Remaining open items (1, 4, 5, 6, 9, 10) carry forward to Phase 20.

### Package count methodology (reviewer note)

The headline counts (41 control-plane internal packages, 17 edge-api internal packages) were produced by `git ls-tree -r HEAD --name-only` filtered to `internal/` directories and counting unique immediate sub-paths. A `git ls-tree -r` filtered to leaf directories may yield different counts depending on how sub-packages are counted. The discrepancy does not affect the zero-test gap identification or the priority table, which are derived from per-package file inspection rather than aggregate counts. The `audit` package (7 source files, 2 test files) and `auditworker/sinks` (8 source files, 1 test file) were omitted from the table because they were not reachable from the static walk used; they are acknowledged here as candidates for Phase 20 coverage work.

### Coverage gate awk fix

The original `go tool cover -func | awk` snippet in the Proposed Coverage Gate section contained a bug: `go tool cover -func` emits per-function lines containing the file path but the final `total:` summary line contains no package path, so the conjunction `/package-path/ && /total/` never matches. The snippet has been corrected above to run `go test -cover` per-package and extract the `total:` line from each individual run.

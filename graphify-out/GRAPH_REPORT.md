# Graph Report - agent-ae621fbbf21eec699  (2026-06-25)

## Corpus Check
- 2801 files · ~3,105,188 words
- Verdict: corpus is large enough that graph structure adds value.

## Summary
- 85 nodes · 149 edges · 13 communities (3 shown, 10 thin omitted)
- Extraction: 89% EXTRACTED · 11% INFERRED · 0% AMBIGUOUS · INFERRED: 16 edges (avg confidence: 0.8)
- Token cost: 0 input · 0 output

## Graph Freshness
- Built from commit: `b3b3b332`
- Run `git rev-parse HEAD` and compare to check if the graph is stale.
- Run `graphify update .` after code changes (no API cost).

## Community Hubs (Navigation)
- [[_COMMUNITY_Community 0|Community 0]]
- [[_COMMUNITY_Community 1|Community 1]]
- [[_COMMUNITY_Community 2|Community 2]]
- [[_COMMUNITY_Community 3|Community 3]]
- [[_COMMUNITY_Community 4|Community 4]]
- [[_COMMUNITY_Community 5|Community 5]]
- [[_COMMUNITY_Community 6|Community 6]]
- [[_COMMUNITY_Community 7|Community 7]]
- [[_COMMUNITY_Community 8|Community 8]]
- [[_COMMUNITY_Community 9|Community 9]]
- [[_COMMUNITY_Community 10|Community 10]]
- [[_COMMUNITY_Community 11|Community 11]]
- [[_COMMUNITY_Community 12|Community 12]]

## God Nodes (most connected - your core abstractions)
1. `New()` - 16 edges
2. `main()` - 16 edges
3. `newArchiver()` - 13 edges
4. `newFakeStore()` - 12 edges
5. `makeRows()` - 11 edges
6. `fakeRepo` - 8 edges
7. `PgRepository` - 8 edges
8. `Archiver` - 5 edges
9. `TestArchiveSelectsOldRowsOnly()` - 5 edges
10. `TestArchiveWritesCompressedJSONL()` - 5 edges

## Surprising Connections (you probably didn't know these)
- `main()` --calls--> `NewPgRepository()`  [INFERRED]
  apps/control-plane/cmd/server/main.go → apps/control-plane/internal/auditarchive/pg_repository.go
- `main()` --calls--> `NewStorageObjectStore()`  [INFERRED]
  apps/control-plane/cmd/server/main.go → apps/control-plane/internal/auditarchive/storage_store.go
- `main()` --calls--> `New()`  [INFERRED]
  apps/control-plane/cmd/server/main.go → apps/control-plane/internal/auditarchive/archiver.go
- `newArchiver()` --calls--> `New()`  [INFERRED]
  apps/control-plane/internal/auditarchive/archiver_test.go → apps/control-plane/internal/auditarchive/archiver.go
- `TestArchiveSelectsOldRowsOnly()` --calls--> `New()`  [INFERRED]
  apps/control-plane/internal/auditarchive/archiver_test.go → apps/control-plane/internal/auditarchive/archiver.go

## Communities (13 total, 10 thin omitted)

### Community 0 - "Community 0"
Cohesion: 0.26
Nodes (13): envOr(), loadStorageConfigFromEnv(), main(), newAccountsAccessChecker(), newAccountsNamer(), parseDurationEnv(), parseIntEnv(), resolveLiteLLMBaseURL() (+5 more)

### Community 1 - "Community 1"
Cohesion: 0.21
Nodes (9): Archiver, groupByMonth(), sortBySeq(), sortedMonths(), AuditRow, Config, ManifestEntry, ObjectStore (+1 more)

### Community 2 - "Community 2"
Cohesion: 0.48
Nodes (15): New(), makeRows(), newArchiver(), newFakeStore(), TestArchiveChainIntegrityPreserved(), TestArchiveIdempotent(), TestArchiveManifestRecorded(), TestArchiveSelectsOldRowsOnly() (+7 more)

## Knowledge Gaps
- **6 isolated node(s):** `AuditRow`, `ManifestEntry`, `Repository`, `ObjectStore`, `Config` (+1 more)
  These have ≤1 connection - possible missing edges or undocumented components.
- **10 thin communities (<3 nodes) omitted from report** — run `graphify query` to explore isolated nodes.

## Suggested Questions
_Questions this graph is uniquely positioned to answer:_

- **Why does `main()` connect `Community 0` to `Community 2`, `Community 3`, `Community 5`, `Community 7`?**
  _High betweenness centrality (0.636) - this node is a cross-community bridge._
- **Why does `New()` connect `Community 2` to `Community 0`, `Community 1`, `Community 6`?**
  _High betweenness centrality (0.630) - this node is a cross-community bridge._
- **Why does `NewPgRepository()` connect `Community 3` to `Community 0`?**
  _High betweenness centrality (0.194) - this node is a cross-community bridge._
- **Are the 14 inferred relationships involving `New()` (e.g. with `.Put()` and `newArchiver()`) actually correct?**
  _`New()` has 14 INFERRED edges - model-reasoned connections that need verification._
- **Are the 3 inferred relationships involving `main()` (e.g. with `New()` and `NewPgRepository()`) actually correct?**
  _`main()` has 3 INFERRED edges - model-reasoned connections that need verification._
- **What connects `AuditRow`, `ManifestEntry`, `Repository` to the rest of the system?**
  _6 weakly-connected nodes found - possible documentation gaps or missing edges._
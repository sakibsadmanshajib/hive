---
phase: 20-provider-catalog
plan: 03
type: execute
wave: 2
depends_on: [20-01]
size: M
branch: b/phase-20-provider-catalog
milestone: v1.1
track: A
files_modified:
  - apps/control-plane/internal/litellmconfig/generator.go
  - apps/control-plane/internal/litellmconfig/generator_test.go
  - apps/control-plane/internal/litellmconfig/restart.go
  - apps/control-plane/internal/litellmconfig/restart_test.go
  - deploy/docker/docker-compose.yml
  - deploy/litellm/config.yaml
  - .env.example
autonomous: true
---

# Plan 20-03 — LiteLLM Config Generation + Controlled Restart

## Objective

Implement a control-plane subsystem that generates `deploy/litellm/config.yaml` from the current state of `provider_routes` and `custom_providers` tables, then triggers a controlled LiteLLM proxy restart so new providers become live without manual intervention.

**Verified mechanism (do not re-research):** LiteLLM has NO `POST /reload` endpoint. The file-based config path (`deploy/litellm/config.yaml`) requires a proxy restart on change. The zero-downtime alternative is DB-backed config (`DATABASE_URL` + `general_settings.store_model_in_db: true`) enabling live mutation via admin API (`POST /model/new`, `/model/update`, `/model/delete`, master key auth). This plan implements file-based restart as the primary path and documents the DB-backed alternative for executor decision at build time.

---

## Tasks

### Task 1: Config generator

**File:** `apps/control-plane/internal/litellmconfig/generator.go`

Package `litellmconfig` exposes:

```go
type ModelEntry struct {
    ModelName   string
    LiteLLMName string   // e.g. "openrouter/openai/gpt-4o"
    APIBase     string
    APIKeyEnv   string
}

type Config struct {
    Models         []ModelEntry
    GeneralSettings GeneralSettings
}

type GeneralSettings struct {
    MasterKey string
}

// Generate builds a LiteLLM config.yaml byte slice from the provided model entries.
// It does NOT read from DB itself; the caller supplies the entries.
func Generate(cfg Config) ([]byte, error)

// WriteAndRestart writes the generated config to the given path and signals a restart.
func WriteAndRestart(ctx context.Context, configPath string, cfg Config, restarter Restarter) error
```

The YAML output must match the structure LiteLLM expects:

```yaml
model_list:
  - model_name: <ModelEntry.ModelName>
    litellm_params:
      model: <ModelEntry.LiteLLMName>
      api_base: <ModelEntry.APIBase>
      api_key: "os.environ/<ModelEntry.APIKeyEnv>"

general_settings:
  master_key: <cfg.GeneralSettings.MasterKey>
```

Use `gopkg.in/yaml.v3` (already present in the module or add it — check `go.mod`).

---

### Task 2: Restart mechanism

**File:** `apps/control-plane/internal/litellmconfig/restart.go`

```go
type Restarter interface {
    Restart(ctx context.Context) error
}

// DockerRestarter signals a LiteLLM container restart via the Docker socket.
// It calls `docker restart <containerName>` using exec.CommandContext.
// containerName read from env var LITELLM_CONTAINER_NAME (default: "litellm").
type DockerRestarter struct { ContainerName string }

func (r DockerRestarter) Restart(ctx context.Context) error
```

The control-plane container must have access to the Docker socket. This is wired via compose volume mount (Task 5 below).

**Timeout:** wrap the `docker restart` call in a 30-second context deadline. Log outcome at INFO level (success) or ERROR level (failure). On failure, return the error to the caller; the caller is responsible for alerting.

**Alternative path (document, do not implement unless executor decides):** If the operator sets `LITELLM_CONFIG_MODE=db` in the environment, the subsystem should instead call `POST /model/new` (and `/update`, `/delete`) on the LiteLLM admin API using the `LITELLM_MASTER_KEY`. Document this path in a `// TODO(litellm-db-mode)` comment block in `restart.go` with the required env vars and API contract. The executor must re-confirm the exact `/model/*` API shape via Context7 at build time before implementing.

---

### Task 3: Service integration

**File:** `apps/control-plane/internal/litellmconfig/generator.go` (or a new `service.go`)

Expose a `SyncService` that:

1. Queries `custom_providers` (enabled only) and `provider_routes` from the DB.
2. Builds `[]ModelEntry` by joining routes with their provider's `base_url`, `api_key_env`, `litellm_prefix`.
3. Calls `Generate` to produce YAML bytes.
4. Calls `WriteAndRestart`.

Wire `SyncService` into the control-plane HTTP surface as:

```
POST /internal/litellm/sync
```

Protected by shared-secret middleware. Returns 200 on success, 500 with error JSON on failure. This endpoint is called by the provider CRUD handlers (Plan 20-02) after any create/update/delete.

---

### Task 4: Unit tests

**File:** `apps/control-plane/internal/litellmconfig/generator_test.go`

TDD:

1. `Generate` with two model entries produces valid YAML with correct `model_list` length.
2. `Generate` with empty entries produces an empty `model_list` (not nil YAML).
3. `Generate` output parses back without error via `yaml.Unmarshal`.
4. `WriteAndRestart` calls `Restarter.Restart` exactly once on success.
5. `WriteAndRestart` does NOT call `Restarter.Restart` if `Generate` fails.

**File:** `apps/control-plane/internal/litellmconfig/restart_test.go`

Use a `MockRestarter` implementing `Restarter` to verify call count and error propagation. Do NOT call real `docker restart` in unit tests.

---

### Task 5: Docker Compose volume wiring

**File:** `deploy/docker/docker-compose.yml`

The control-plane service must:

- Mount the Docker socket: `- /var/run/docker.sock:/var/run/docker.sock:ro`
- Mount the LiteLLM config directory as a shared volume so the generated file is visible to the LiteLLM container:
  ```yaml
  volumes:
    - litellm-config:/etc/litellm
  ```
  The LiteLLM container must already mount the same volume at the path it reads from (`--config /etc/litellm/config.yaml`).
- Add env var: `LITELLM_CONTAINER_NAME: ${LITELLM_CONTAINER_NAME:-litellm}`
- Add env var: `LITELLM_CONFIG_PATH: ${LITELLM_CONFIG_PATH:-/etc/litellm/config.yaml}`

Add `litellm-config` to the top-level `volumes:` block.

Read the existing compose file before editing to ensure the additions integrate consistently with current service definitions.

---

### Task 6: .env.example additions

**File:** `.env.example`

Add a `# === LiteLLM config generation ===` block:

```
LITELLM_CONTAINER_NAME=litellm
LITELLM_CONFIG_PATH=/etc/litellm/config.yaml
LITELLM_MASTER_KEY=                   # required — set before deploying
# LITELLM_CONFIG_MODE=db              # uncomment to use DB-backed live-reload instead of file+restart
```

---

## TDD Notes

Generator is pure (no I/O) and trivially unit-testable. Restarter interface decouples from Docker in tests. Write tests before implementing `Generate` and `WriteAndRestart`.

---

## Acceptance Criteria

- [ ] `Generate` produces valid LiteLLM YAML for any non-empty `[]ModelEntry`.
- [ ] `WriteAndRestart` calls `Restarter.Restart` after successful write; skips on generate failure.
- [ ] `DockerRestarter.Restart` calls `docker restart <name>` with 30-second timeout.
- [ ] `POST /internal/litellm/sync` returns 200 and the LiteLLM container restarts within 35 seconds.
- [ ] Docker socket mounted read-only into control-plane container in compose.
- [ ] Shared `litellm-config` volume wired between control-plane and litellm containers.
- [ ] `.env.example` documents all new env vars including the `LITELLM_CONFIG_MODE=db` alternative.
- [ ] All 5 generator unit tests + 2 restart unit tests pass.
- [ ] Executor note: confirm DB-backed `/model/*` API shape via Context7 before deciding on `LITELLM_CONFIG_MODE=db` implementation.

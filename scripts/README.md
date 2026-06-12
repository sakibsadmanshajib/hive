# Hive EnterpriseEdge Installer

One-line installer for a self-hosted Hive EnterpriseEdge box.

## Quick Start

```sh
curl -fsSL https://raw.githubusercontent.com/sakibsadmanshajib/hive/main/scripts/install.sh | bash
```

With local Ollama inference:

```sh
curl -fsSL https://raw.githubusercontent.com/sakibsadmanshajib/hive/main/scripts/install.sh | bash -s -- --with-ollama
```

## Requirements

- Ubuntu or Debian, x86_64 or arm64
- Root or a user with `sudo` access
- Outbound internet access (to pull Docker images and clone the repo)
- A [Supabase](https://supabase.com) project with S3 storage enabled and `hive-files` / `hive-images` buckets created
- At least one LLM provider key: `OPENROUTER_API_KEY`, `GROQ_API_KEY`, or `--with-ollama`

## Flags

| Flag | Description |
|------|-------------|
| `--with-ollama` | Enable in-stack Ollama local inference. Triggers the hardware-aware model advisor, sets `OLLAMA_BASE_URL=http://ollama:11434`, and uncomments ollama entries in `deploy/litellm/config.yaml`. |
| `--uninstall` | Stop the stack and print what remains for manual cleanup. Does not delete source files or volumes. |
| `--non-interactive` | Skip all prompts. Read all values from environment variables. |
| `--help` | Show usage. |

## Environment Overrides

| Variable | Default | Purpose |
|----------|---------|---------|
| `HIVE_HOME` | `/opt/hive` | Where the repo is cloned / updated |
| `OLLAMA_MODEL` | (advisor-selected) | Override the model advisor selection. Example: `OLLAMA_MODEL=llama3.1:8b` |

All `.env` values can be pre-set as shell environment variables before running the installer. Required values in non-interactive mode:

**Required:**

- `SUPABASE_URL`
- `SUPABASE_ANON_KEY`
- `SUPABASE_SERVICE_ROLE_KEY`
- `SUPABASE_DB_URL`
- `S3_ENDPOINT`
- `S3_ACCESS_KEY`
- `S3_SECRET_KEY`
- At least one of: `OPENROUTER_API_KEY`, `GROQ_API_KEY`, or `--with-ollama`

**Optional (auto-generated if blank):**

- `CONTROL_PLANE_INTERNAL_TOKEN` (auto-generated via `openssl rand`)
- `LITELLM_MASTER_KEY` (auto-generated)
- `GRAFANA_ADMIN_USER` / `GRAFANA_ADMIN_PASSWORD`
- `S3_REGION` (defaults to `us-east-1`)

## Non-interactive Example

```sh
export SUPABASE_URL=https://xxxx.supabase.co
export SUPABASE_ANON_KEY=eyJ...
export SUPABASE_SERVICE_ROLE_KEY=eyJ...
export SUPABASE_DB_URL=postgres://postgres:password@db.xxxx.supabase.co:5432/postgres
export S3_ENDPOINT=https://xxxx.supabase.co/storage/v1/s3
export S3_ACCESS_KEY=...
export S3_SECRET_KEY=...
export OPENROUTER_API_KEY=sk-or-...

curl -fsSL https://raw.githubusercontent.com/sakibsadmanshajib/hive/main/scripts/install.sh | bash -s -- --non-interactive
```

## What the Installer Does

1. Detects OS and architecture (fails politely if unsupported).
2. Installs Docker CE plus the Compose plugin via the official Docker apt repository if not already present.
3. Clones the repo to `$HIVE_HOME` on first run, or does `git fetch + reset --hard origin/main` on subsequent runs (idempotent).
4. Copies `.env.example` to `.env` and runs a configuration wizard (TTY) or reads from environment (non-TTY / `--non-interactive`). Existing `.env` files are left untouched on re-runs.
5. If `--with-ollama`: runs the hardware-aware model advisor (see below), sets `OLLAMA_BASE_URL=http://ollama:11434`, and uncomments ollama model entries in `deploy/litellm/config.yaml`. Appends the advisor-selected model as a named LiteLLM route.
6. Runs `docker compose --profile enterprise up -d --build` from `deploy/docker`.
7. Health-polls `edge-api :8080/health` and `control-plane :8081/health` with a 120-second timeout, then prints a success banner with all service URLs or actionable diagnostics on failure.

## Services Started

| Service | URL |
|---------|-----|
| Edge API | http://localhost:8080 |
| Control Plane | http://localhost:8081 |
| Web Console | http://localhost:3000 |
| Open WebUI | http://localhost:3003 |
| LiteLLM | http://localhost:4000 |

## Uninstall

```sh
bash /opt/hive/scripts/install.sh --uninstall
```

This stops the Docker Compose stack. It does NOT delete:

- `$HIVE_HOME` (source code and your `.env`)
- Docker images (remove with `docker image prune` or individually)
- Docker volumes (remove with `docker volume prune`)
- The Docker runtime itself

## Idempotency

Re-running the installer is safe:

- Docker installation is skipped if already present.
- The repo is updated to `origin/main` rather than re-cloned.
- An existing `.env` file is reused without modification.
- The stack is restarted (`up -d --build` is idempotent for Compose).

## Hardware-Aware Model Advisor

When `--with-ollama` is set the installer runs a two-tier hardware detection step before starting the stack.

**Tier 1 (always runs, no extra dependencies):** reads `/proc/meminfo` for total RAM and calls `nvidia-smi` for VRAM if an NVIDIA GPU is present. Maps detected hardware to a recommended Ollama model tag:

| Hardware tier | Ollama tag | Disk size | Notes |
|---------------|------------|-----------|-------|
| CPU-only / less than 12 GB RAM | `phi4-mini` | 2.5 GB | 3.8B, 128k context, tools support |
| 8 GB VRAM | `qwen3:8b` | 5.2 GB | Strong multilingual, Bangla-capable |
| 12 to 16 GB VRAM | `qwen2.5:14b` | 9.0 GB | Strong instruction following |
| 24+ GB VRAM | `qwen3:32b` | 20 GB | Thinking and tools support |
| 128 GB unified memory | `qwen2.5:72b` | 47 GB | Datacentre / workstation tier |

Note: `llama3.3` on Ollama is a 70B model only. Use `qwen3:8b` or `llama3.1:8b` for 8 GB VRAM slots.

**Tier 2 (optional, requires Node.js >= 16):** if Node.js is available on the host, the advisor runs `npx llm-checker hw-detect` for a richer GPU inventory and scored model analysis. `llm-checker` (npm package by Pavelevich) analyses over 200 Ollama models across quality, speed, and context dimensions. The bash detection result remains the authoritative default; llm-checker output is shown for operator reference.

The operator can accept the recommendation, enter a different Ollama tag, or bypass the advisor entirely by setting `OLLAMA_MODEL=<tag>` before running the installer. In TTY mode the installer offers to pull the chosen model immediately after the stack reaches healthy status. In non-interactive or piped mode it prints the pull command for later use.

To override the advisor and pull a specific model non-interactively:

```sh
export OLLAMA_MODEL=qwen3:8b
curl -fsSL https://raw.githubusercontent.com/sakibsadmanshajib/hive/main/scripts/install.sh | bash -s -- --with-ollama --non-interactive
```

## Design Notes

The script wraps all logic inside `main()` called at the very last line. This means a truncated partial download from `curl | bash` will never execute any code (pattern from Ollama and uv installers). Secret prompts (API keys, tokens, passwords) disable terminal echo via POSIX `stty -echo` while typing, with echo restored afterwards and on interrupt, so secrets never land in terminal scrollback. Dynamic variable assignments use `eval "${var}=\$value"` indirection so user input is never re-parsed by the shell. The script passes `shellcheck -S warning` (SC2048, SC2086, SC1091 suppressed with inline comments where correct).

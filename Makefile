.PHONY: gen-permissions agent-sif test-scripts

# Codegen for the permissions registry → TypeScript mirror.
# Runs inside the `toolchain` profile container (Go + tools). The toolchain
# service consumes no variables from the project .env, so we pass --env-file
# only when it exists (local dev convenience); CI runs without it.
ENV_FILE := $(abspath .env)
COMPOSE_ENV_ARG := $(if $(wildcard $(ENV_FILE)),--env-file $(ENV_FILE),)

gen-permissions:
	cd deploy/docker && docker compose $(COMPOSE_ENV_ARG) --profile local --profile tools run --rm --entrypoint /bin/sh toolchain -c "cd /workspace && /usr/local/go/bin/go run ./apps/control-plane/cmd/gen-permissions ./apps/web-console/lib/control-plane/permissions.generated.ts"

# Build the agent-engine Apptainer image (agent-engine.sif) on a host that has
# apptainer installed (linux/amd64). CI builds the same image via
# .github/workflows/agent-engine-sif.yml. See deploy/apptainer/README.md.
agent-sif:
	deploy/apptainer/build.sh

# Self-checks for the repo's operational Python scripts (no framework, no
# network). These guard credential-rotation ordering and .env rewriting, where
# a regression strands a deployment on a revoked key.
test-scripts:
	python3 scripts/redact-log-credentials.py --selfcheck
	python3 scripts/test_seed_owui_e2e_user.py
	python3 scripts/test_seed_demo_owner.py
	python3 scripts/test_install_owui_jwt_forward.py
	python3 scripts/test_owui_rag_env_config.py
	python3 scripts/test_owui_ui_surfaces.py
	python3 scripts/test_caddy_owui_blocklist.py
	python3 scripts/test_owui_model_picker_filter.py
	python3 scripts/generate-enterprise-jwt-keys.py --self-check
	python3 scripts/register-owui-oauth-client.py --self-check
	python3 scripts/test_caddy_supabase_routes.py
	python3 scripts/test_selfhost_supabase_seam.py
	python3 scripts/check-env-supabase-target.py --self-check

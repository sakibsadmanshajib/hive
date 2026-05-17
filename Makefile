.PHONY: gen-permissions

# Codegen for the permissions registry → TypeScript mirror.
# Runs inside the `toolchain` profile container (Go + tools). The toolchain
# service consumes no variables from the project .env, so we pass --env-file
# only when it exists (local dev convenience); CI runs without it.
ENV_FILE := $(abspath .env)
COMPOSE_ENV_ARG := $(if $(wildcard $(ENV_FILE)),--env-file $(ENV_FILE),)

gen-permissions:
	cd deploy/docker && docker compose $(COMPOSE_ENV_ARG) --profile local --profile tools run --rm --entrypoint /bin/sh toolchain -c "cd /workspace && /usr/local/go/bin/go run ./apps/control-plane/cmd/gen-permissions ./apps/web-console/lib/control-plane/permissions.generated.ts"

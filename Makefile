.PHONY: gen-permissions

gen-permissions:
	cd deploy/docker && docker compose --env-file ../../.env --profile local --profile tools run --rm --entrypoint /bin/sh toolchain -c "cd /workspace && /usr/local/go/bin/go run ./apps/control-plane/cmd/gen-permissions ./apps/web-console/lib/control-plane/permissions.generated.ts"

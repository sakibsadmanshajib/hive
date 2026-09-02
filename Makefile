.PHONY: gen-permissions agent-sif test-scripts test-owui-frontend go-cover

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
	python3 scripts/test_shared_billing_mapping.py
	python3 scripts/test_install_owui_jwt_forward.py
	python3 scripts/test_owui_rag_env_config.py
	python3 scripts/test_owui_chat_system_prompt.py
	python3 scripts/test_owui_embedding_burst.py
	python3 scripts/test_owui_ui_surfaces.py
	python3 scripts/test_owui_display_name.py
	python3 scripts/test_owui_tenant_role.py
	python3 scripts/test_owui_knowledge_authz.py
	python3 scripts/test_owui_chat_delete_authz.py
	python3 scripts/test_owui_chat_delete_task_cancel.py
	python3 scripts/test_owui_bulk_chat_authz.py
	python3 scripts/test_owui_audio_error_leak.py
	python3 scripts/test_owui_ydoc_task_cancel.py
	python3 scripts/test_owui_main_admin_flag.py
	python3 scripts/test_shared_demo_account.py
	python3 scripts/test_owui_skill_group_grants.py
	python3 scripts/test_owui_skill_tenant_scope.py
	python3 scripts/test_owui_task_upstream_auth.py
	python3 scripts/owui-promote-instance-admin.py --self-check
	python3 scripts/classify-upstream-refusal.py --selfcheck
	python3 scripts/extract-sdk-failures.py --selfcheck
	python3 scripts/report-free-pool-health.py --selfcheck
	python3 scripts/test_caddy_owui_blocklist.py
	python3 scripts/test_caddy_one_front_door.py
	python3 scripts/test_caddy_upstream_retry.py
	python3 scripts/test_owui_model_picker_filter.py
	python3 scripts/test_owui_chat_list_page_size.py
	python3 scripts/generate-enterprise-jwt-keys.py --self-check
	python3 scripts/register-owui-oauth-client.py --self-check
	python3 scripts/test_check_oauth_scopes.py
	python3 scripts/test_caddy_supabase_routes.py
	python3 scripts/test_compose_port_bindings.py
	python3 scripts/test_selfhost_supabase_seam.py
	python3 scripts/test_litellm_config_seam.py
	python3 scripts/check-env-supabase-target.py --self-check
	python3 scripts/derive-pooler-dsn.py --self-test
	python3 scripts/test_caddy_console_auth_origin.py --self-check
	python3 scripts/restore-storage-objects.py --self-check
	python3 scripts/test_owui_oauth_client_auth.py
	python3 scripts/test_owui_agent_proxy.py
	python3 scripts/test_owui_oauth_callback_landing.py
	python3 scripts/test_owui_chat_error_detail.py
	python3 scripts/test_owui_task_nonstreaming_response.py
	python3 scripts/test_owui_internal_metadata_boundary.py
	python3 scripts/test_owui_embed_attribution.py

# Node, not python, and it downloads a pinned vitest (plus its coverage
# provider), so it is deliberately not folded into test-scripts, which is pure
# python self-checks with no network.
test-owui-frontend:
	sh scripts/test-owui-hive-frontend.sh

# Umbrella Go coverage across both modules in one command. Each module's tests
# write binary coverage into its own GOCOVERDIR (-coverpkg=./... instruments
# every package of the module, so unreached packages count as uncovered rather
# than silently vanishing); `go tool covdata textfmt` merges the directories
# into profiles that `go tool cover -func` summarizes into one total line per
# module plus a true cross-module umbrella total. No thresholds are enforced;
# this makes the number one command away so a threshold decision has something
# to stand on. Runs in the toolchain container per the Docker-only testing
# contract; -count=1 -short matches the standard go-tests invocation.
go-cover:
	cd deploy/docker && docker compose $(COMPOSE_ENV_ARG) --profile tools run --rm toolchain \
	  "rm -rf /tmp/hive-go-cover && mkdir -p /tmp/hive-go-cover/control-plane /tmp/hive-go-cover/edge-api && \
	   cd /workspace/apps/control-plane && go test ./... -count=1 -short -coverpkg=./... -covermode=atomic -args -test.gocoverdir=/tmp/hive-go-cover/control-plane && \
	   cd ../edge-api && go test ./... -count=1 -short -coverpkg=./... -covermode=atomic -args -test.gocoverdir=/tmp/hive-go-cover/edge-api && \
	   go tool covdata textfmt -i=/tmp/hive-go-cover/control-plane -o=/tmp/hive-go-cover/control-plane.out && \
	   go tool covdata textfmt -i=/tmp/hive-go-cover/edge-api -o=/tmp/hive-go-cover/edge-api.out && \
	   go tool covdata textfmt -i=/tmp/hive-go-cover/control-plane,/tmp/hive-go-cover/edge-api -o=/tmp/hive-go-cover/umbrella.out && \
	   printf 'control-plane '; go tool cover -func=/tmp/hive-go-cover/control-plane.out | tail -1 && \
	   printf 'edge-api      '; go tool cover -func=/tmp/hive-go-cover/edge-api.out | tail -1 && \
	   printf 'UMBRELLA      '; go tool cover -func=/tmp/hive-go-cover/umbrella.out | tail -1"

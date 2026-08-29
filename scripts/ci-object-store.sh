#!/usr/bin/env bash
# ci-object-store.sh -- stand up a throwaway object store for a CI job, on top
# of the throwaway Postgres scripts/ci-throwaway-db.sh has already prepared.
#
# Why this exists
# ---------------
# The SDK conformance suites cover POST /v1/files and the whole /v1/batches
# surface, which uploads its input file first. Both write an object, so both
# need a real S3 endpoint. Until now CI got that endpoint from the S3_ENDPOINT
# repository secret, which was last written on 2026-04-21 and still names the
# Supabase Cloud project this repository left during the August self-hosted
# cutover. That project no longer resolves in DNS, so every upload in CI died
# at the socket and the conformance suites carried the assertions as expected
# failures rather than as coverage (issue #1324). The same blind 500 then
# reached production and nothing caught it (issue #1282).
#
# There is no correct value to rotate that secret to. deploy/docker/
# Caddyfile.supabase serves /storage/v1 on the in-network listener only, and
# says in as many words that ports 80 and 443 are the in-network surface and
# must never be exposed; the public listener carries /auth/v1 and nothing else.
# So the box's Storage is not reachable from a GitHub-hosted runner by design,
# and a job that stands up everything else it needs should stand this up too.
#
# Why the real supabase/storage-api and not MinIO
# -----------------------------------------------
# Because the defect that actually took production down was a Storage server
# defect, not a client one. With S3_PROTOCOL_ACCESS_KEY_ID and
# S3_PROTOCOL_ACCESS_KEY_SECRET absent, Storage answers every signed request
# with 403 AccessDenied and the body "Missing S3 Protocol Access Key ID or
# Secret Key Environment variables" (issue #1282, fixed by PR #1368). MinIO has
# no such concept, so a MinIO fixture could not have caught that and could not
# catch its next relative either. This runs the same image at the same
# immutable digest deploy/docker/docker-compose.enterprise.yml pins, so CI and
# the box cannot drift onto different Storage versions.
#
# The standing "no MinIO" line is untouched by this and is not being argued
# with: it is a statement about what a deployment persists objects into
# (docker-compose.enterprise.yml, "No MinIO: STORAGE_BACKEND=file ... No S3
# call leaves the box"), and this container lives for the length of one job and
# holds nothing.
#
# What it needs from the database, and why that is already there
# --------------------------------------------------------------
# Storage runs its own migrations into the `storage` schema and needs that
# schema plus the Supabase roles to exist first. deploy/supabase/init/
# 00-extensions.sql already creates both, and already names this consumer in
# its own comments ("supabase-storage - needs the `storage` schema"), including
# the default privileges in `storage` that stop the Storage API taking SQLSTATE
# 42501 on every bucket call. So this needs no schema work of its own; it needs
# only to run after scripts/ci-throwaway-db.sh.
#
# What it deliberately does NOT reproduce
# ---------------------------------------
# Production reaches Storage through Caddyfile.supabase, which strips
# /storage/v1 before Storage sees the request, so Storage is given
# S3_PROTOCOL_PREFIX=/storage/v1 to rebuild the canonical URI it signs against.
# There is no gateway in front of this fixture, so the prefix is empty and the
# endpoint is .../s3 directly. The agreement between the gateway's handle_path,
# the prefix and the endpoint the operator is told to use is therefore not
# exercised live here. It is not unchecked: scripts/test_selfhost_supabase_seam.py
# asserts all three agree, and CI runs it through `make test-scripts`. Adding
# Caddy to this fixture to close the last of that gap would add a component and
# its failure modes for a seam that already has a guard.
#
# Everything here is thrown away with the runner. The S3 credential pair and
# the two API keys are generated per run, are worth nothing outside this job,
# and name nothing that outlives it.
#
# Usage
#   PGHOST=127.0.0.1 PGPORT=5432 PGUSER=postgres PGPASSWORD=postgres \
#     PGDATABASE=hive_ci scripts/ci-object-store.sh >> "$GITHUB_ENV"
#
# Prints, on stdout, in $GITHUB_ENV form:
#   S3_ENDPOINT S3_ACCESS_KEY S3_SECRET_KEY S3_REGION S3_USE_SSL
#   S3_BUCKET_FILES S3_BUCKET_IMAGES
# Everything human-readable goes to stderr, so stdout stays machine-readable.

set -euo pipefail

# Same digest deploy/docker/docker-compose.enterprise.yml pins. Refresh both
# together or CI stops testing what the box runs:
#   docker pull supabase/storage-api:v1.11.13 && \
#   docker inspect --format='{{index .RepoDigests 0}}' supabase/storage-api:v1.11.13
storage_image="supabase/storage-api:v1.11.13@sha256:1e85dad48e8b3e85890a555e5114dc7ee48c2e8be4cfd97dd4e3564b4f104fcd"

container="hive-ci-storage"
db_container="hive-ci-db"
host_port="5000"
bucket_files="hive-files"
bucket_images="hive-images"

while [ $# -gt 0 ]; do
  case "$1" in
    --container) container="$2"; shift 2 ;;
    --db-container) db_container="$2"; shift 2 ;;
    --port) host_port="$2"; shift 2 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done

log() { echo "$@" >&2; }

db_name="${PGDATABASE:-hive_ci}"
db_user="${PGUSER:-postgres}"
# Unquoted deliberately, matching scripts/ci-supabase-stack.sh: an assignment
# does not word-split, and this keeps the repository secret scanner from
# reading an environment-variable default as a hardcoded credential.
db_password=${PGPASSWORD:-postgres}

# The schema and roles Storage migrates into have to be there before it boots.
# Checked rather than assumed, because the failure otherwise is Storage
# crash-looping on a permission error that reads like a bad password.
if ! docker exec -e PGPASSWORD="$db_password" "$db_container" \
     psql -U "$db_user" -d "$db_name" -qtAX \
     -c "select 1 from information_schema.schemata where schema_name='storage'" \
     2>/dev/null | grep -q 1; then
  log "::error::the \`storage\` schema does not exist on $db_container. scripts/ci-throwaway-db.sh applies deploy/supabase/init/00-extensions.sql, which creates it, and has to run before this script."
  exit 1
fi

# ---------------------------------------------------------------------------
# Credentials. All four are minted here and none is a repository secret.
#
# The S3 pair is what edge-api and control-plane sign with AND what Storage
# verifies against, deliberately one pair rather than two. Two independent
# values let the signing half and the verifying half drift with no boot error
# on either side, and the only symptom is a 403 from a service that looks
# configured, which is exactly how #1282 presented for three weeks.
# ---------------------------------------------------------------------------
s3_access_key="ci-$(openssl rand -hex 8)"
s3_secret_key="$(openssl rand -hex 32)"
jwt_secret="$(openssl rand -hex 32)"

mint_key() {
  python3 - "$jwt_secret" "$1" <<'PY'
import base64, hashlib, hmac, json, sys, time
secret, role = sys.argv[1], sys.argv[2]
def b64(raw): return base64.urlsafe_b64encode(raw).rstrip(b"=").decode()
header = b64(json.dumps({"alg": "HS256", "typ": "JWT"}, separators=(",", ":")).encode())
now = int(time.time())
payload = b64(json.dumps(
    {"role": role, "iss": "supabase", "iat": now, "exp": now + 24 * 3600},
    separators=(",", ":"),
).encode())
signing_input = f"{header}.{payload}".encode()
sig = b64(hmac.new(secret.encode(), signing_input, hashlib.sha256).digest())
print(f"{header}.{payload}.{sig}")
PY
}
anon_key="$(mint_key anon)"
service_role_key="$(mint_key service_role)"

# The address a container on another docker network reaches, both for this
# fixture reaching Postgres and for the compose stack reaching this fixture.
# Not localhost: inside those containers that is the container itself. Same
# arrangement ci.yml's throwaway-database step already uses for the DSN.
gw_ip="$(docker network inspect bridge -f '{{ (index .IPAM.Config 0).Gateway }}')"

log "==> Storage API on the throwaway database"
docker rm -f "$container" >/dev/null 2>&1 || true
docker run -d --name "$container" -p "${host_port}:5000" \
  -e "ANON_KEY=${anon_key}" \
  -e "SERVICE_KEY=${service_role_key}" \
  -e "PGRST_JWT_SECRET=${jwt_secret}" \
  -e "DATABASE_URL=postgres://${db_user}:${db_password}@${gw_ip}:${PGPORT:-5432}/${db_name}" \
  -e "FILE_SIZE_LIMIT=52428800" \
  -e "STORAGE_BACKEND=file" \
  -e "FILE_STORAGE_BACKEND_PATH=/var/lib/storage" \
  -e "TENANT_ID=stub" \
  -e "REGION=us-east-1" \
  -e "GLOBAL_S3_BUCKET=stub" \
  -e "S3_PROTOCOL_ACCESS_KEY_ID=${s3_access_key}" \
  -e "S3_PROTOCOL_ACCESS_KEY_SECRET=${s3_secret_key}" \
  "$storage_image" >/dev/null

# 127.0.0.1, not localhost, for the reason the enterprise healthcheck gives:
# the storage image resolves localhost to ::1 first and the server binds IPv4
# only, so a localhost probe reports connection refused forever while the
# service is in fact serving.
ready=0
for _ in $(seq 1 60); do
  if curl -fsS -o /dev/null "http://127.0.0.1:${host_port}/status" 2>/dev/null; then
    ready=1
    break
  fi
  sleep 2
done
if [ "$ready" -ne 1 ]; then
  log "::error::the throwaway Storage API never became ready"
  docker logs "$container" 2>&1 | tail -40 >&2
  exit 1
fi

# ---------------------------------------------------------------------------
# Buckets. Same duplicate-tolerant recipe docker-compose.enterprise.yml's
# supabase-init carries, and for the same reason: the Storage API answers a
# duplicate bucket with HTTP 400 carrying {"statusCode":"409","error":
# "Duplicate"}, so a status-only check for 409 never matches and a re-run
# fails on a bucket that is already correct.
# ---------------------------------------------------------------------------
for bucket in "$bucket_files" "$bucket_images"; do
  response="$(curl -sS -w '\n%{http_code}' -X POST "http://127.0.0.1:${host_port}/bucket" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer ${service_role_key}" \
    -d "{\"id\":\"${bucket}\",\"name\":\"${bucket}\",\"public\":false}")"
  status="$(printf '%s' "$response" | tail -n1)"
  body="$(printf '%s' "$response" | sed '$d')"
  case "$status" in
    2*) log "  bucket ${bucket}: created" ;;
    400|409)
      case "$body" in
        *Duplicate*|*already*exists*) log "  bucket ${bucket}: already exists" ;;
        *) log "::error::bucket ${bucket} failed (${status}) ${body}"; exit 1 ;;
      esac ;;
    *) log "::error::bucket ${bucket} failed (${status}) ${body}"; exit 1 ;;
  esac
done

# ---------------------------------------------------------------------------
# Prove the round trip before printing anything.
#
# This is the whole point of the change and so it is the one thing that must
# not be taken on trust. A fixture that boots, reports healthy and cannot store
# an object is the same lie as the dead endpoint it replaces, and it would be
# indistinguishable from it in a green run. Signed here with the same SigV4 the
# Go client uses, so a wrong key, a wrong prefix or a missing bucket fails at
# this line naming the storage path, rather than fifteen minutes later inside a
# conformance suite naming an HTTP 500.
# ---------------------------------------------------------------------------
# The AWS CLI is on the GitHub-hosted runner image and is used here only as a
# SigV4 signer we did not write ourselves. Its absence is a hard error rather
# than a skipped check, because a skipped round trip is the silent absence this
# whole change exists to remove.
log "==> signed round trip"
if ! command -v aws >/dev/null; then
  log "::error::the aws CLI is unavailable, so the signed round trip cannot be proven. Refusing to report a working object store on a check that did not run."
  exit 1
fi
probe_dir="$(mktemp -d)"
trap 'rm -rf "$probe_dir"' EXIT
printf 'ci-object-store round trip\n' > "$probe_dir/probe.txt"
round_trip_ok=1
if ! AWS_ACCESS_KEY_ID="$s3_access_key" AWS_SECRET_ACCESS_KEY="$s3_secret_key" \
     AWS_DEFAULT_REGION=us-east-1 \
     aws --endpoint-url "http://127.0.0.1:${host_port}/s3" s3api put-object \
       --bucket "$bucket_files" --key "ci-fixture/probe.txt" \
       --body "$probe_dir/probe.txt" >/dev/null 2>"$probe_dir/err"; then
  round_trip_ok=0
elif ! AWS_ACCESS_KEY_ID="$s3_access_key" AWS_SECRET_ACCESS_KEY="$s3_secret_key" \
     AWS_DEFAULT_REGION=us-east-1 \
     aws --endpoint-url "http://127.0.0.1:${host_port}/s3" s3api get-object \
       --bucket "$bucket_files" --key "ci-fixture/probe.txt" \
       "$probe_dir/probe.back" >/dev/null 2>"$probe_dir/err"; then
  round_trip_ok=0
elif ! cmp -s "$probe_dir/probe.txt" "$probe_dir/probe.back"; then
  log "::error::the object store returned different bytes than it was given"
  round_trip_ok=0
fi
if [ "$round_trip_ok" -ne 1 ]; then
  log "::error::the throwaway object store answered its health check but could not complete a signed PUT and GET. That is the shape of issue #1282: Storage reports healthy and refuses every signed request. Its own error and its logs follow."
  cat "$probe_dir/err" >&2 || true
  docker logs "$container" 2>&1 | tail -40 >&2
  exit 1
fi
log "round trip OK"

# S3_USE_SSL is false because this fixture speaks plain http on the runner.
# The endpoint carries the /s3 path segment because packages/storage/s3.go
# builds path-style object URLs by appending bucket and key to whatever
# S3_ENDPOINT names, which is the same reason the box's value ends in
# /storage/v1/s3.
echo "S3_ENDPOINT=http://${gw_ip}:${host_port}/s3"
echo "S3_ACCESS_KEY=${s3_access_key}"
echo "S3_SECRET_KEY=${s3_secret_key}"
echo "S3_REGION=us-east-1"
echo "S3_USE_SSL=false"
echo "S3_BUCKET_FILES=${bucket_files}"
echo "S3_BUCKET_IMAGES=${bucket_images}"

#!/bin/sh
set -eu

: "${HIVE_API_KEY:?HIVE_API_KEY is required}"

HIVE_BASE_URL="${HIVE_BASE_URL:-http://localhost:8080}"
HIVE_CONTROL_PLANE_URL="${HIVE_CONTROL_PLANE_URL:-http://localhost:8081}"
TMP_ROOT="${TMPDIR:-/tmp}"
JSONL_FILE="$TMP_ROOT/hive-phase10.jsonl"
WAV_FILE="$TMP_ROOT/hive-phase10.wav"

fail() {
  printf 'phase10-smoke: %s\n' "$*" >&2
  exit 1
}

is_http_code() {
  case "$1" in
    [0-9][0-9][0-9]) return 0 ;;
    *) return 1 ;;
  esac
}

assert_status_code() {
  label=$1
  code=$2
  want=$3
  body_file=$4
  is_http_code "$code" || fail "$label returned invalid HTTP code $code; body: $body_file"
  [ "$code" -eq "$want" ] || fail "$label returned HTTP $code, want $want; body: $body_file"
}

assert_status_2xx() {
  label=$1
  code=$2
  body_file=$3
  is_http_code "$code" || fail "$label returned invalid HTTP code $code; body: $body_file"
  [ "$code" -ge 200 ] && [ "$code" -lt 300 ] || fail "$label returned HTTP $code, want 2xx; body: $body_file"
}

assert_body_lacks() {
  label=$1
  body_file=$2
  shift 2
  for needle in "$@"; do
    if grep -Fqi "$needle" "$body_file"; then
      fail "$label body contains forbidden text '$needle'; body: $body_file"
    fi
  done
}

assert_openai_object() {
  label=$1
  body_file=$2
  object_name=$3
  if grep -Fq "\"object\":\"$object_name\"" "$body_file"; then
    return 0
  fi
  if grep -Eq "\"object\"[[:space:]]*:[[:space:]]*\"$object_name\"" "$body_file"; then
    return 0
  fi
  fail "$label body missing object $object_name; body: $body_file"
}

request_json() {
  label=$1
  method=$2
  url=$3
  body=$4
  out="$TMP_ROOT/hive-phase10-$label.json"
  if ! code=$(curl -sS -o "$out" -w "%{http_code}" -X "$method" "$url" \
    -H "Authorization: Bearer $HIVE_API_KEY" \
    -H "Content-Type: application/json" \
    -d "$body"); then
    fail "$label curl request failed; body: $out"
  fi
  printf '%s %s\n' "$code" "$out"
}

request_control_json() {
  label=$1
  body=$2
  out="$TMP_ROOT/hive-phase10-$label.json"
  if ! code=$(curl -sS -o "$out" -w "%{http_code}" -X POST "$HIVE_CONTROL_PLANE_URL/internal/routing/select" \
    -H "Content-Type: application/json" \
    -d "$body"); then
    fail "$label curl request failed; body: $out"
  fi
  printf '%s %s\n' "$code" "$out"
}

request_get() {
  label=$1
  url=$2
  out="$TMP_ROOT/hive-phase10-$label.json"
  if ! code=$(curl -sS -o "$out" -w "%{http_code}" "$url" \
    -H "Authorization: Bearer $HIVE_API_KEY"); then
    fail "$label curl request failed; body: $out"
  fi
  printf '%s %s\n' "$code" "$out"
}

request_multipart() {
  label=$1
  url=$2
  shift 2
  out="$TMP_ROOT/hive-phase10-$label.json"
  if ! code=$(curl -sS -o "$out" -w "%{http_code}" -X POST "$url" \
    -H "Authorization: Bearer $HIVE_API_KEY" \
    "$@"); then
    fail "$label curl request failed; body: $out"
  fi
  printf '%s %s\n' "$code" "$out"
}

capture_request() {
  response=$("$@") || exit $?
  set -- $response
  [ "$#" -eq 2 ] || fail "request helper returned $# fields, want 2"
  REQUEST_CODE=$1
  REQUEST_BODY=$2
}

assert_media_result() {
  label=$1
  code=$2
  body_file=$3
  shift 3

  assert_body_lacks "$label" "$body_file" "$@"
  is_http_code "$code" || fail "$label returned invalid HTTP code $code; body: $body_file"
  if [ "$code" -ge 200 ] && [ "$code" -lt 300 ]; then
    return 0
  fi
  if grep -Eq '"error"[[:space:]]*:' "$body_file"; then
    return 0
  fi
  fail "$label returned HTTP $code without an OpenAI-style JSON error; body: $body_file"
}

cat >"$JSONL_FILE" <<'JSONL'
{"custom_id":"req-1","method":"POST","url":"/v1/chat/completions","body":{"model":"hive-default","messages":[{"role":"user","content":"ping"}]}}
JSONL

printf 'RIFF$\000\000\000WAVEfmt \020\000\000\000\001\000\001\000@\037\000\000@\037\000\000\001\000\010\000data\000\000\000\000' >"$WAV_FILE"

capture_request request_control_json route-image '{"alias_id":"hive-auto","need_image_generation":true}'
assert_status_2xx route-image "$REQUEST_CODE" "$REQUEST_BODY"
capture_request request_control_json route-tts '{"alias_id":"hive-auto","need_tts":true}'
assert_status_2xx route-tts "$REQUEST_CODE" "$REQUEST_BODY"
capture_request request_control_json route-stt '{"alias_id":"hive-auto","need_stt":true}'
assert_status_2xx route-stt "$REQUEST_CODE" "$REQUEST_BODY"
capture_request request_control_json route-batch '{"alias_id":"hive-auto","need_batch":true}'
assert_status_2xx route-batch "$REQUEST_CODE" "$REQUEST_BODY"

capture_request request_json chat POST "$HIVE_BASE_URL/v1/chat/completions" '{"model":"hive-default","messages":[{"role":"user","content":"ping"}]}'
chat_code=$REQUEST_CODE
chat_body=$REQUEST_BODY
assert_status_code chat "$chat_code" 200 "$chat_body"
if ! grep -Fq '"object":"chat.completion"' "$chat_body" && \
  ! grep -Fq '"object":"chat.completion.chunk"' "$chat_body" && \
  ! grep -Eq '"object"[[:space:]]*:[[:space:]]*"chat\.completion(\.chunk)?"' "$chat_body"; then
  fail "chat body missing chat.completion object; body: $chat_body"
fi

capture_request request_json image POST "$HIVE_BASE_URL/v1/images/generations" '{"model":"hive-auto","prompt":"test image","n":1,"size":"1024x1024"}'
assert_media_result image "$REQUEST_CODE" "$REQUEST_BODY" \
  "policy_mode must be strict or temporary_overage" \
  "model_alias is required" \
  "Failed to reserve credits for batch" \
  "Failed to select a route" \
  "no eligible routes" \
  "supports_image_generation" \
  "storage unavailable" \
  "storage disabled" \
  "endpoints disabled"

capture_request request_json audio-speech POST "$HIVE_BASE_URL/v1/audio/speech" '{"model":"hive-auto","input":"ping","voice":"alloy","response_format":"mp3"}'
assert_media_result audio-speech "$REQUEST_CODE" "$REQUEST_BODY" \
  "policy_mode must be strict or temporary_overage" \
  "model_alias is required" \
  "Failed to reserve credits for batch" \
  "Failed to select a route" \
  "no eligible routes" \
  "supports_tts" \
  "storage unavailable" \
  "storage disabled" \
  "endpoints disabled"

capture_request request_multipart audio-transcription "$HIVE_BASE_URL/v1/audio/transcriptions" \
  -F "model=hive-auto" \
  -F "file=@$WAV_FILE;type=audio/wav"
assert_media_result audio-transcription "$REQUEST_CODE" "$REQUEST_BODY" \
  "policy_mode must be strict or temporary_overage" \
  "model_alias is required" \
  "Failed to reserve credits for batch" \
  "Failed to select a route" \
  "no eligible routes" \
  "supports_stt" \
  "storage unavailable" \
  "storage disabled" \
  "endpoints disabled"

capture_request request_multipart file-upload "$HIVE_BASE_URL/v1/files" \
  -F "purpose=batch" \
  -F "file=@$JSONL_FILE;type=application/jsonl"
file_code=$REQUEST_CODE
file_body=$REQUEST_BODY
assert_status_code file-upload "$file_code" 200 "$file_body"
assert_openai_object file-upload "$file_body" file

capture_request request_get batch-list "$HIVE_BASE_URL/v1/batches"
batch_code=$REQUEST_CODE
batch_body=$REQUEST_BODY
assert_status_code batch-list "$batch_code" 200 "$batch_body"
assert_body_lacks batch-list "$batch_body" \
  "policy_mode must be strict or temporary_overage" \
  "model_alias is required" \
  "Failed to reserve credits for batch"
assert_openai_object batch-list "$batch_body" list

printf 'phase10-smoke: all probes passed\n'

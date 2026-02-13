#!/usr/bin/env bash
set -euo pipefail

API_BASE="${API_BASE:-http://localhost:8080}"
COMPOSE_CMD="${COMPOSE_CMD:-docker compose}"
CREDENTIAL_SCRIPT="${CREDENTIAL_SCRIPT:-./scripts/get-test-credentials.sh}"
RUN_RATE_LIMIT_TEST="${RUN_RATE_LIMIT_TEST:-1}"
KEEP_TEST_DATA="${KEEP_TEST_DATA:-0}"
DB_USER="${DB_USER:-}"
DB_NAME="${DB_NAME:-}"

TMP_DIR="$(mktemp -d)"
TMP_BODY="${TMP_DIR}/body"
TMP_HEADERS="${TMP_DIR}/headers"

cleanup() {
  rm -rf "$TMP_DIR"

  if [[ "${KEEP_TEST_DATA}" == "1" ]]; then
    return 0
  fi

  if [[ -n "${SMOKE_USER_ID:-}" ]]; then
    local cleanup_db_user cleanup_db_name
    cleanup_db_user="${SMOKE_DB_USER:-${DB_USER:-altpocket}}"
    cleanup_db_name="${SMOKE_DB_NAME:-${DB_NAME:-altpocket}}"
    $COMPOSE_CMD exec -T db psql -U "$cleanup_db_user" -d "$cleanup_db_name" -c "DELETE FROM users WHERE id='${SMOKE_USER_ID}'; DELETE FROM tags t WHERE NOT EXISTS (SELECT 1 FROM item_tags it WHERE it.tag_id=t.id);" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

info() {
  printf "[info] %s\n" "$*"
}

fail() {
  printf "[fail] %s\n" "$*" >&2
  if [[ -f "$TMP_BODY" ]]; then
    printf "[fail] response body: %s\n" "$(cat "$TMP_BODY")" >&2
  fi
  exit 1
}

request() {
  local method="$1"
  local url="$2"
  local data="$3"
  shift 3

  local -a curl_args
  curl_args=(
    -sS
    -o "$TMP_BODY"
    -D "$TMP_HEADERS"
    -w '%{http_code}'
    -X "$method"
    "$url"
  )

  if [[ -n "$data" ]]; then
    curl_args+=( -H 'Content-Type: application/json' --data "$data" )
  fi

  while (($#)); do
    curl_args+=( -H "$1" )
    shift
  done

  curl "${curl_args[@]}"
}

assert_status() {
  local actual="$1"
  local expected="$2"
  local label="$3"
  [[ "$actual" == "$expected" ]] || fail "$label: expected $expected, got $actual"
}

assert_body_contains() {
  local needle="$1"
  local label="$2"
  grep -Fq "$needle" "$TMP_BODY" || fail "$label: expected body to contain '$needle'"
}

assert_header_contains() {
  local needle="$1"
  local label="$2"
  grep -iFq "$needle" "$TMP_HEADERS" || fail "$label: expected headers to contain '$needle'"
}

json_string_field() {
  local field="$1"
  sed -n "s/.*\"${field}\":\"\([^\"]*\)\".*/\1/p" "$TMP_BODY" | head -n1
}

wait_for_api() {
  info "Waiting for API at $API_BASE/healthz"
  for _ in $(seq 1 30); do
    if code=$(request GET "$API_BASE/healthz" ""); then
      if [[ "$code" == "200" ]]; then
        if [[ "$(cat "$TMP_BODY")" == "ok" ]]; then
          info "API is ready"
          return 0
        fi
      fi
    fi
    sleep 1
  done
  fail "API did not become ready within 30s"
}

provision_credentials() {
  info "Provisioning smoke test credentials"
  if [[ ! -x "$CREDENTIAL_SCRIPT" ]]; then
    fail "credential script is missing or not executable: $CREDENTIAL_SCRIPT"
  fi

  # shellcheck disable=SC1090
  eval "$($CREDENTIAL_SCRIPT)"

  [[ -n "${SMOKE_USER_ID:-}" ]] || fail "SMOKE_USER_ID is empty"
  [[ -n "${SMOKE_CSRF_TOKEN:-}" ]] || fail "SMOKE_CSRF_TOKEN is empty"
  [[ -n "${SMOKE_SESSION_COOKIE:-}" ]] || fail "SMOKE_SESSION_COOKIE is empty"
  [[ -n "${SMOKE_JWT_TOKEN:-}" ]] || fail "SMOKE_JWT_TOKEN is empty"

  if [[ -n "${SMOKE_DB_USER:-}" ]]; then
    DB_USER="$SMOKE_DB_USER"
  fi
  if [[ -n "${SMOKE_DB_NAME:-}" ]]; then
    DB_NAME="$SMOKE_DB_NAME"
  fi
}

new_test_url() {
  printf 'https://example.com/%s/%s/%s' "$1" "$(date +%s)" "$RANDOM"
}

run_rate_limit_test() {
  local hit_429=0

  info "Rate limit test (POST /v1/items, expect 429 after burst)"
  for i in $(seq 1 60); do
    local url code
    url="$(new_test_url rate-limit-${i})"
    code=$(request POST "$API_BASE/v1/items" "{\"url\":\"${url}\",\"tags\":[\"rate\"]}" "Authorization: Bearer ${SMOKE_JWT_TOKEN}")

    if [[ "$code" == "429" ]]; then
      hit_429=1
      break
    fi

    if [[ "$code" != "200" ]]; then
      fail "rate limit test: expected 200/429, got $code"
    fi
  done

  [[ "$hit_429" == "1" ]] || fail "rate limit test: expected at least one 429"
}

main() {
  wait_for_api

  info "GET /healthz should return 200 and body 'ok'"
  code=$(request GET "$API_BASE/healthz" "")
  assert_status "$code" "200" "GET /healthz"
  [[ "$(cat "$TMP_BODY")" == "ok" ]] || fail "GET /healthz body mismatch"

  info "OPTIONS /v1/items should return CORS preflight 204"
  code=$(request OPTIONS "$API_BASE/v1/items" "" "Origin: http://localhost:3000" "Access-Control-Request-Method: POST")
  assert_status "$code" "204" "OPTIONS /v1/items"
  assert_header_contains 'Access-Control-Allow-Origin: *' "OPTIONS /v1/items"

  info "GET /v1/auth/google/login should redirect and set oauth_state"
  code=$(request GET "$API_BASE/v1/auth/google/login" "")
  assert_status "$code" "302" "GET /v1/auth/google/login"
  assert_header_contains 'Location:' "GET /v1/auth/google/login"
  assert_header_contains 'Set-Cookie: oauth_state=' "GET /v1/auth/google/login"

  info "GET /v1/auth/google/callback without valid state should return 400"
  code=$(request GET "$API_BASE/v1/auth/google/callback" "")
  assert_status "$code" "400" "GET /v1/auth/google/callback"

  info "POST /v1/auth/extension/exchange with invalid payload should return 400"
  code=$(request POST "$API_BASE/v1/auth/extension/exchange" '{}' )
  assert_status "$code" "400" "POST /v1/auth/extension/exchange invalid payload"

  info "POST /v1/auth/extension/exchange with invalid token should return 401"
  code=$(request POST "$API_BASE/v1/auth/extension/exchange" '{"id_token":"invalid"}')
  assert_status "$code" "401" "POST /v1/auth/extension/exchange invalid token"

  info "GET /v1/items without auth should return 401"
  code=$(request GET "$API_BASE/v1/items" "")
  assert_status "$code" "401" "GET /v1/items without auth"

  info "POST /v1/items without auth should return 403 (csrf)"
  code=$(request POST "$API_BASE/v1/items" '{"url":"https://example.com"}')
  assert_status "$code" "403" "POST /v1/items without auth"

  provision_credentials

  local session_headers
  session_headers=(
    "Cookie: ${SMOKE_SESSION_COOKIE}"
    "X-CSRF-Token: ${SMOKE_CSRF_TOKEN}"
  )

  local item_url item_id
  item_url="$(new_test_url session-create)"

  info "POST /v1/items with session+csrf should create item"
  code=$(request POST "$API_BASE/v1/items" "{\"url\":\"${item_url}\",\"tags\":[\"Go\",\"Backend\"]}" "${session_headers[0]}" "${session_headers[1]}")
  assert_status "$code" "200" "POST /v1/items with session"
  assert_body_contains '"created":true' "POST /v1/items with session"
  item_id="$(json_string_field item_id)"
  [[ -n "$item_id" ]] || fail "POST /v1/items did not return item_id"

  info "GET /v1/items with session should include created item"
  code=$(request GET "$API_BASE/v1/items?sort=newest" "" "${session_headers[0]}")
  assert_status "$code" "200" "GET /v1/items with session"
  assert_body_contains '"items":' "GET /v1/items with session"
  assert_body_contains '"pagination":' "GET /v1/items with session"
  assert_body_contains '"per_page":' "GET /v1/items with session"
  assert_body_contains '"user_id":"' "GET /v1/items with session"
  assert_body_contains "$item_id" "GET /v1/items with session"

  info "GET /v1/items/{id} with session should return item"
  code=$(request GET "$API_BASE/v1/items/${item_id}" "" "${session_headers[0]}")
  assert_status "$code" "200" "GET /v1/items/{id} with session"
  assert_body_contains '"id":"' "GET /v1/items/{id} with session"
  assert_body_contains '"canonical_url":"' "GET /v1/items/{id} with session"
  assert_body_contains '"content_full":' "GET /v1/items/{id} with session"
  assert_body_contains '"tags":' "GET /v1/items/{id} with session"
  assert_body_contains "$item_id" "GET /v1/items/{id} with session"

  info "GET /v1/tags?q=go with session should return normalized tag"
  code=$(request GET "$API_BASE/v1/tags?q=go" "" "${session_headers[0]}")
  assert_status "$code" "200" "GET /v1/tags with session"
  assert_body_contains '"name":"go"' "GET /v1/tags with session"
  assert_body_contains '"normalized_name":"go"' "GET /v1/tags with session"

  info "POST /v1/items/{id}/refetch with session+csrf should return 202"
  code=$(request POST "$API_BASE/v1/items/${item_id}/refetch" "" "${session_headers[0]}" "${session_headers[1]}")
  assert_status "$code" "202" "POST /v1/items/{id}/refetch with session"
  assert_body_contains '"status":"queued"' "POST /v1/items/{id}/refetch with session"

  info "DELETE /v1/items/{id} with session+csrf should return 204"
  code=$(request DELETE "$API_BASE/v1/items/${item_id}" "" "${session_headers[0]}" "${session_headers[1]}")
  assert_status "$code" "204" "DELETE /v1/items/{id} with session"

  info "GET /v1/items/{id} after delete should return 404"
  code=$(request GET "$API_BASE/v1/items/${item_id}" "" "${session_headers[0]}")
  assert_status "$code" "404" "GET /v1/items/{id} after delete"

  local bearer_item_url bearer_item_id
  bearer_item_url="$(new_test_url bearer-create)"

  info "POST /v1/items with bearer token should create item"
  code=$(request POST "$API_BASE/v1/items" "{\"url\":\"${bearer_item_url}\",\"tags\":[\"Api\",\"Smoke\"]}" "Authorization: Bearer ${SMOKE_JWT_TOKEN}")
  assert_status "$code" "200" "POST /v1/items with bearer"
  assert_body_contains '"created":true' "POST /v1/items with bearer"
  bearer_item_id="$(json_string_field item_id)"
  [[ -n "$bearer_item_id" ]] || fail "POST /v1/items with bearer did not return item_id"

  info "POST /v1/items/{id}/refetch with bearer should return 202"
  code=$(request POST "$API_BASE/v1/items/${bearer_item_id}/refetch" "" "Authorization: Bearer ${SMOKE_JWT_TOKEN}")
  assert_status "$code" "202" "POST /v1/items/{id}/refetch with bearer"

  info "DELETE /v1/items/{id} with bearer should return 204"
  code=$(request DELETE "$API_BASE/v1/items/${bearer_item_id}" "" "Authorization: Bearer ${SMOKE_JWT_TOKEN}")
  assert_status "$code" "204" "DELETE /v1/items/{id} with bearer"

  info "POST /v1/items with session but missing csrf should return 403"
  code=$(request POST "$API_BASE/v1/items" "{\"url\":\"$(new_test_url csrf-missing)\"}" "Cookie: ${SMOKE_SESSION_COOKIE}")
  assert_status "$code" "403" "POST /v1/items with missing csrf"

  info "GET /ui/items with session should return 200"
  code=$(request GET "$API_BASE/ui/items" "" "Cookie: ${SMOKE_SESSION_COOKIE}")
  assert_status "$code" "200" "GET /ui/items with session"
  assert_body_contains '<!DOCTYPE html>' "GET /ui/items with session"

  if [[ "$RUN_RATE_LIMIT_TEST" == "1" ]]; then
    run_rate_limit_test
  fi

  info "All smoke tests passed"
}

main

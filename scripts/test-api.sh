#!/usr/bin/env bash
set -euo pipefail

API_BASE="${API_BASE:-http://localhost:8080}"
TMP_BODY="${TMPDIR:-/tmp}/altpocket_api_test_body"

info() {
  printf "[info] %s\n" "$*"
}

fail() {
  printf "[fail] %s\n" "$*" >&2
  exit 1
}

request() {
  local method="$1"
  local url="$2"
  local data="${3:-}"
  local content_type="${4:-}"

  if [[ -n "$content_type" ]]; then
    curl -sS -o "$TMP_BODY" -w '%{http_code}' -X "$method" "$url" -H "Content-Type: $content_type" -d "$data"
  else
    curl -sS -o "$TMP_BODY" -w '%{http_code}' -X "$method" "$url"
  fi
}

wait_for_api() {
  info "Waiting for API at $API_BASE/healthz"
  for _ in $(seq 1 30); do
    if code=$(request GET "$API_BASE/healthz"); then
      if [[ "$code" == "200" ]]; then
        body=$(cat "$TMP_BODY")
        if [[ "$body" == "ok" ]]; then
          info "API is ready"
          return 0
        fi
      fi
    fi
    sleep 1
  done
  fail "API did not become ready within 30s"
}

main() {
  wait_for_api

  info "GET /healthz should return 200 and body 'ok'"
  code=$(request GET "$API_BASE/healthz")
  [[ "$code" == "200" ]] || fail "expected 200, got $code"
  body=$(cat "$TMP_BODY")
  [[ "$body" == "ok" ]] || fail "expected body 'ok', got '$body'"

  info "GET /v1/items without auth should return 401"
  code=$(request GET "$API_BASE/v1/items")
  [[ "$code" == "401" ]] || fail "expected 401, got $code"

  info "POST /v1/items without auth should return 403 (CSRF)"
  code=$(request POST "$API_BASE/v1/items" '{"url":"https://example.com"}' 'application/json')
  [[ "$code" == "403" ]] || fail "expected 403, got $code"

  info "POST /v1/auth/extension/exchange with invalid payload should return 400"
  code=$(request POST "$API_BASE/v1/auth/extension/exchange" '{}' 'application/json')
  [[ "$code" == "400" ]] || fail "expected 400, got $code"

  info "GET /ui/items without session should redirect (302)"
  code=$(request GET "$API_BASE/ui/items")
  [[ "$code" == "302" ]] || fail "expected 302, got $code"

  info "All tests passed"
}

main

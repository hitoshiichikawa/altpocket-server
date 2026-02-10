#!/usr/bin/env bash
set -euo pipefail

API_BASE="${API_BASE:-http://localhost:8080}"
COMPOSE_CMD="${COMPOSE_CMD:-docker compose}"
DB_SERVICE="${DB_SERVICE:-db}"
DB_USER="${DB_USER:-altpocket}"
DB_NAME="${DB_NAME:-altpocket}"
JWT_SECRET="${JWT_SECRET:-}"
SMOKE_PREFIX="${SMOKE_PREFIX:-smoke}"

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    printf '[fail] required command not found: %s\n' "$1" >&2
    exit 1
  fi
}

info() {
  printf '[info] %s\n' "$*" >&2
}

b64url() {
  openssl base64 -A | tr '+/' '-_' | tr -d '='
}

jwt_hs256() {
  local secret="$1"
  local user_id="$2"
  local now exp header payload unsigned signature

  now=$(date +%s)
  exp=$((now + 86400))
  header='{"alg":"HS256","typ":"JWT"}'
  payload=$(printf '{"sub":"%s","iat":%d,"exp":%d}' "$user_id" "$now" "$exp")

  unsigned="$(printf '%s' "$header" | b64url).$(printf '%s' "$payload" | b64url)"
  signature=$(printf '%s' "$unsigned" | openssl dgst -sha256 -hmac "$secret" -binary | b64url)
  printf '%s.%s\n' "$unsigned" "$signature"
}

derive_jwt_secret() {
  local value
  value="$($COMPOSE_CMD config | awk '
    /^  api:$/ { in_api=1; next }
    in_api && /^  [a-zA-Z0-9_-]+:$/ { in_api=0 }
    in_api && /^      JWT_SECRET:/ { print $2; exit }
  ' || true)"

  if [[ -n "$value" ]]; then
    printf '%s\n' "$value"
    return 0
  fi

  printf 'change-me\n'
}

psql_scalar() {
  local sql="$1"
  $COMPOSE_CMD exec -T "$DB_SERVICE" \
    psql -U "$DB_USER" -d "$DB_NAME" -Atq -v ON_ERROR_STOP=1 -c "$sql" | head -n1 | tr -d '[:space:]'
}

main() {
  require_cmd curl
  require_cmd openssl

  if [[ -z "$JWT_SECRET" ]]; then
    JWT_SECRET="$(derive_jwt_secret)"
    info "JWT secret was not provided; derived from compose config"
  fi

  local code
  code=$(curl -sS -o /dev/null -w '%{http_code}' "$API_BASE/healthz" || true)
  if [[ "$code" != "200" ]]; then
    printf '[fail] API is not reachable at %s (status=%s)\n' "$API_BASE" "$code" >&2
    exit 1
  fi

  local nonce google_sub email name avatar user_id csrf_token session_id jwt
  nonce="$(date +%s)-$RANDOM-$RANDOM"
  google_sub="${SMOKE_PREFIX}-sub-${nonce}"
  email="${SMOKE_PREFIX}+${nonce}@example.com"
  name="${SMOKE_PREFIX}-user-${nonce}"
  avatar='https://example.com/avatar.png'

  user_id="$(psql_scalar "INSERT INTO users (google_sub, email, name, avatar_url) VALUES ('${google_sub}', '${email}', '${name}', '${avatar}') RETURNING id;")"
  if [[ -z "$user_id" ]]; then
    printf '[fail] failed to create smoke test user\n' >&2
    exit 1
  fi

  csrf_token="$(openssl rand -hex 24)"
  session_id="$(psql_scalar "INSERT INTO sessions (user_id, csrf_token, expires_at) VALUES ('${user_id}', '${csrf_token}', NOW() + interval '7 days') RETURNING id;")"
  if [[ -z "$session_id" ]]; then
    printf '[fail] failed to create smoke test session\n' >&2
    exit 1
  fi

  jwt="$(jwt_hs256 "$JWT_SECRET" "$user_id")"

  printf 'SMOKE_USER_ID=%q\n' "$user_id"
  printf 'SMOKE_CSRF_TOKEN=%q\n' "$csrf_token"
  printf 'SMOKE_SESSION_ID=%q\n' "$session_id"
  printf 'SMOKE_SESSION_COOKIE=%q\n' "altpocket_session=${session_id}"
  printf 'SMOKE_JWT_TOKEN=%q\n' "$jwt"
}

main

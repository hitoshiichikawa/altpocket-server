# Docker API Smoke Test

## Purpose
Run an end-to-end smoke test against the local Docker Compose stack over `http://localhost:8080`.

## Prerequisites
- Docker daemon is running
- Services are started:

```bash
docker compose up -d db
docker compose exec -T db psql -U altpocket -d altpocket < migrations/001_init.sql
docker compose up --build -d api worker
```

## Run
```bash
API_BASE=http://localhost:8080 ./scripts/test-api.sh
```

Windows (PowerShell 7+):
```powershell
$env:API_BASE = "http://localhost:8080"
.\scripts\test-api.ps1
```

## Credential Strategy (implemented)
`./scripts/get-test-credentials.sh` acquires test credentials without external OAuth dependency:
- Inserts a temporary test user into PostgreSQL
- Creates a temporary web session (`altpocket_session`) + CSRF token
- Issues an HS256 JWT for the same user (uses `JWT_SECRET`; derives from `docker compose config` when not explicitly set)
- Exports shell variables used by `./scripts/test-api.sh`

PowerShell variant uses `./scripts/get-test-credentials.ps1` and `./scripts/test-api.ps1`.

The smoke test uses both auth paths:
- Session cookie + CSRF header
- Bearer JWT

By default, the temporary test user data is deleted automatically at script exit.
Set `KEEP_TEST_DATA=1` to keep it for debugging.

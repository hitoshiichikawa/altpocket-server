# Repository Guidelines

## Project Structure & Module Organization
- `cmd/api`: API + Web UI (SSR) entrypoint.
- `cmd/worker`: async fetch worker entrypoint.
- `internal/`: app logic (auth, store, URL normalization, fetcher, UI rendering, rate limiting).
- `templates/` + `static/`: SSR templates and vanilla JS/CSS assets.
- `migrations/`: SQL migrations (start with `001_init.sql`).
- `extension/`: Chrome extension (MV3 popup UI).
- `deploy/`: Dockerfiles for API/worker.

## Build, Test, and Development Commands
- `go test ./...`: run unit tests (none yet; add as you extend).
- `docker compose up --build api worker`: build and run API + worker.
- `psql ... -f migrations/001_init.sql`: apply schema.

## Coding Style & Naming Conventions
- Go code uses standard formatting (`gofmt`).
- Prefer `snake_case` for SQL objects and JSON fields.
- Keep handlers thin; move DB logic into `internal/store`.

## Testing Guidelines
- Add tests under `internal/*` packages; name files `*_test.go`.
- Favor table-driven tests for URL normalization, tag normalization, and search queries.

## Commit & Pull Request Guidelines
- No established history yet; use concise, imperative commits (e.g., "Add item fetcher").
- PRs should include: summary, test output (or rationale if skipped), and relevant screenshots for UI/extension changes.

## Security & Configuration Tips
- Secrets (`SESSION_SECRET`, `JWT_SECRET`, OAuth client secrets) must be env vars.
- Web sessions are stored in DB (`sessions` table); worker cleans up expired rows every minute.
- Do not log tokens, cookies, or raw OAuth responses.

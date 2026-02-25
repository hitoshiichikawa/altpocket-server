# Repository Guidelines

## Project Structure & Module Organization
- `cmd/api`: API + Web UI (SSR) entrypoint.
- `cmd/worker`: async fetch worker entrypoint.
- `internal/`: app logic (auth, store, URL normalization, fetcher, UI rendering, rate limiting).
- `templates/` + `static/`: SSR templates and vanilla JS/CSS assets.
- `migrations/`: SQL migrations (start with `001_init.sql`).
- `extension/`: Chrome extension (MV3 side panel UI).
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


# AI-DLC and Spec-Driven Development

Kiro-style Spec Driven Development implementation on AI-DLC (AI Development Life Cycle)

## Project Memory
Project memory keeps persistent guidance (steering, specs notes, component docs) so Codex honors your standards each run. Treat it as the long-lived source of truth for patterns, conventions, and decisions.

- Use `.kiro/steering/` for project-wide policies: architecture principles, naming schemes, security constraints, tech stack decisions, api standards, etc.
- Use local `AGENTS.md` files for feature or library context (e.g. `src/lib/payments/AGENTS.md`): describe domain assumptions, API contracts, or testing conventions specific to that folder. Codex auto-loads these when working in the matching path.
- Specs notes stay with each spec (under `.kiro/specs/`) to guide specification-level workflows.

## Project Context

### Paths
- Steering: `.kiro/steering/`
- Specs: `.kiro/specs/`

### Steering vs Specification

**Steering** (`.kiro/steering/`) - Guide AI with project-wide rules and context
**Specs** (`.kiro/specs/`) - Formalize development process for individual features

### Active Specifications
- Check `.kiro/specs/` for active specifications
- Use `/prompts:kiro-spec-status [feature-name]` to check progress

## Development Guidelines
- Think in English, generate responses in Japanese. All Markdown content written to project files (e.g., requirements.md, design.md, tasks.md, research.md, validation reports) MUST be written in the target language configured for this specification (see spec.json.language).

## Minimal Workflow
- Phase 0 (optional): `/prompts:kiro-steering`, `/prompts:kiro-steering-custom`
- Phase 1 (Specification):
  - `/prompts:kiro-spec-init "description"`
  - `/prompts:kiro-spec-requirements {feature}`
  - `/prompts:kiro-validate-gap {feature}` (optional: for existing codebase)
  - `/prompts:kiro-spec-design {feature} [-y]`
  - `/prompts:kiro-validate-design {feature}` (optional: design review)
  - `/prompts:kiro-spec-tasks {feature} [-y]`
- Phase 2 (Implementation): `/prompts:kiro-spec-impl {feature} [tasks]`
  - `/prompts:kiro-validate-impl {feature}` (optional: after implementation)
- Progress check: `/prompts:kiro-spec-status {feature}` (use anytime)

## Development Rules
- 3-phase approval workflow: Requirements → Design → Tasks → Implementation
- Human review required each phase; use `-y` only for intentional fast-track
- Keep steering current and verify alignment with `/prompts:kiro-spec-status`
- Follow the user's instructions precisely, and within that scope act autonomously: gather the necessary context and complete the requested work end-to-end in this run, asking questions only when essential information is missing or the instructions are critically ambiguous.

## Steering Configuration
- Load entire `.kiro/steering/` as project memory
- Default files: `product.md`, `tech.md`, `structure.md`
- Custom files are supported (managed via `/prompts:kiro-steering-custom`)

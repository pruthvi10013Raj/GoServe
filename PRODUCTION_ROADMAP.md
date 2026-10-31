# GoServe — Production Roadmap & AI Agent Execution Plan

> **Works with both VS Code GitHub Copilot (Agent mode) and Google Antigravity.**
> Copy this file and `AGENTS.md` into the repository root. Give the agent **one phase at a
> time**. Never ask an agent to implement the whole roadmap in one go.
>
> Revision notes (v2): tool-neutral wording; shared `AGENTS.md` + `docs/PROGRESS.md`
> handoff so you can switch tools between phases; findings from an independent code
> review merged in; Phase 1B (low-risk hardening) added; CI starts in Phase 1;
> frontend tests added; extra security details for auth, uploads and WebSocket.

---

## 0. How to use this file with each tool

### One-time setup (do this before Phase 0)
1. Put `PRODUCTION_ROADMAP.md` and `AGENTS.md` in the repo root, create `docs/` and commit them.
2. Create a branch per phase, for example `phase-1-refactor`. Merge to `main` only after you review and the acceptance criteria pass. A clean branch is your undo button when you switch tools.
3. Verify the agent really loaded the rules. Start a fresh chat and ask:
   `Summarize AGENTS.md and the Phase discipline rules in 5 bullets. Do not edit anything.`
   If it cannot, attach the files manually (see below).

### VS Code + GitHub Copilot
- Open the repo folder, switch Copilot Chat to **Agent** mode.
- Copilot should pick up `AGENTS.md` automatically. If your VS Code version does not, enable its AGENTS.md instructions setting, or add `.github/copilot-instructions.md` containing one line: `Follow AGENTS.md and PRODUCTION_ROADMAP.md in the repo root.`
- Attach context with `#file:PRODUCTION_ROADMAP.md` (or drag the file into chat), then paste **only the current phase prompt** from section 7.
- Review every diff in the editor / Source Control panel before accepting. Do not use auto-approve for terminal commands that delete files or touch git.

### Google Antigravity
- Open the repo folder as the workspace. `AGENTS.md` in the project root is read as a workspace rule. Do not duplicate it into a second rules file with different wording (Antigravity documents no reliable precedence between `GEMINI.md` and `AGENTS.md`), and keep rule files under its 12,000-character limit.
- Use **Planning** behavior for every phase: let the agent write its implementation plan first, read the plan, and only then approve execution. Keep terminal command approval on "review" so you see what it runs.
- If the agent has a browser tool, use it for UI phases (2, 4, 5, 6, 9) to open the app and click through, but still require honest PASS / FAIL / NOT RUN reporting. A screenshot is not a passing test.
- If rules do not seem to load, paste this one line at the start of the phase prompt: `First read AGENTS.md and PRODUCTION_ROADMAP.md and follow them.`

### Switching tools safely
- **One tool at a time on a working tree.** Do not run Copilot and Antigravity in parallel on the same branch.
- Before switching: ask the current agent to update `docs/PROGRESS.md`, review the diff, and commit (you commit, not the agent).
- The new tool starts with: `Read AGENTS.md, PRODUCTION_ROADMAP.md and docs/PROGRESS.md. Summarize the current state and what Phase N still needs. Do not edit yet.`

### `docs/PROGRESS.md` template (Phase 0 creates it)
```markdown
# GoServe progress log

## Current status
- Active phase: N — name
- Branch: phase-N-...
- Last tool used: Copilot | Antigravity
- Go version / Node version / Docker available: ...

## Decisions made (see roadmap section 11)
| Date | Decision | Reason |

## Phase log
### Phase N — status (NOT STARTED | IN PROGRESS | DONE)
- Changed: files/packages
- Checks: gofmt PASS/FAIL, go vet ..., go test ..., -race ..., npm ..., docker ... (real results)
- Pre-existing failures: ...
- Known risks / follow-ups: ...
- Commit: <hash or message>
```

---

## 1. Repository audit (snapshot inspected 31 Oct 2026)

Repository: https://github.com/pruthvi10013Raj/GoServe — default branch `main`. Re-verify everything below in Phase 0; the repo may have changed.

### Current structure
```text
GoServe/
├── cmd/tcp-client/{main.go,main_test.go}
├── public/{hello.txt,index.html}
├── config.go, config_test.go
├── main.go, main_test.go
├── config.example.yaml
├── go.mod, go.sum
├── README.md
└── .gitignore
```

### Existing functionality to preserve
| Feature | Current implementation |
|---|---|
| HTTPS | Go `net/http`, default port `8443`, cert files `cert/server.crt` + `cert/server.key` |
| Static files | Configurable `./public` directory via `http.FileServer` |
| Upload | `POST /upload`, multipart field `file` |
| Upload auth | HTTP Basic Auth; credentials from `GOSERVE_UPLOAD_USERNAME` / `GOSERVE_UPLOAD_PASSWORD` |
| Upload protections | 10 MiB default total request-body limit, filename sanitization, exclusive file creation (`O_EXCL`), incomplete-file cleanup |
| TCP server | Separate listener, default port `9090`, one newline-terminated message per connection, reply `Received: <msg>` |
| TCP client | `cmd/tcp-client`, default `localhost:9090` |
| TCP limits | 1024-byte message limit, 5-second connection/read/write timeout |
| Config | YAML + CLI; defaults < YAML < explicit CLI flags; legacy aliases `-port`, `-dir` |
| Lifecycle | Header-read timeout 5 s, idle timeout 60 s, graceful shutdown bound 10 s; active TCP connections tracked/closed |
| Tests | Config, HTTP/upload, TCP client/server and shutdown test files exist |

Defaults: HTTPS `8443`, TCP `9090`, static `./public`, uploads `./uploads`, max upload request body `10485760` bytes. `main.go` holds HTTP, upload, TCP and lifecycle logic; `config.go` holds YAML/CLI config. `public/index.html` is a placeholder. The README says the project is not production-ready.

### Gaps visible in source/README
1. Root-level monolithic files need gradual package separation.
2. One shared Basic Auth pair is not a user/account/session system; no roles or per-user ownership.
3. No persistent file metadata, quotas, list/download/delete API or account system.
4. TCP listener is plain and unauthenticated; do not expose it publicly as-is.
5. Browsers cannot open raw TCP. A WebSocket gateway is required for a browser console.
6. No documented production deployment, CI, health/readiness, metrics, backup/restore or runbook.

### Findings from an independent code review (verify each in Phase 0, with file/function evidence)
| # | Finding (from reading `main.go`) | Fixed in |
|---|---|---|
| F1 | Basic Auth compares credentials with `!=` (not constant-time) and has no failed-login rate limit | Phase 1B (stopgap), replaced in Phase 4 |
| F2 | `ParseMultipartForm(MaxUploadSize)` lets each request buffer up to the full limit in RAM; concurrent uploads multiply memory | Phase 1B (smaller in-memory part), replaced by streaming in Phase 5 |
| F3 | `http.Server` has only `ReadHeaderTimeout`/`IdleTimeout`: no body read/write deadlines or `MaxHeaderBytes` (slow-body clients can hold connections) | Phase 1B (per-request deadlines for upload), Phase 7 |
| F4 | TCP listener: one goroutine per connection, no max-connections limit, listens on all interfaces, no auth/TLS | Phase 6 |
| F5 | `http.FileServer` shows directory listings and serves dotfiles inside the static directory | Phase 1B |
| F6 | TLS cert/key paths hardcoded as `cert/server.crt` / `cert/server.key` relative to the working directory | Phase 1B (configurable), Phase 8 (proxy terminates TLS) |
| F7 | Uploaded files created with mode `0666`; unique-name loop is unbounded; Windows reserved names (`CON`, `NUL`, ...) not rejected | Phase 1B (stopgap), Phase 5 (server-generated keys) |
| F8 | Access log does not record the response status code | Phase 1B / Phase 7 |
| F9 | README contains a personal absolute path (`C:\Users\...`) and a stray `test.txt` in the repo root | Phase 1B (docs cleanup) |
| F10 | No CI workflow, LICENSE or Dockerfile present | Phase 1 (CI), Phase 8 (Docker), Phase 9 (LICENSE decision) |

Positives to preserve: env-based credentials, `MaxBytesReader`, `O_EXCL` file creation, partial-file cleanup, safe logging (no paths/headers/bodies), graceful shutdown with tracked TCP connections, bounded TCP framing with deadlines, honest README limitations.

### Baseline before any code change
The agent must run and report **actual output**; do not assume these pass:
```powershell
go version
go test ./...
go vet ./...
go test -race ./...
```
`go.mod` declares Go `1.27.0`; verify the installed toolchain is compatible before changing versions or dependencies. On Windows `-race` needs cgo and a C compiler; if it cannot run locally, record `NOT RUN (reason)` and run it in CI (Linux) or WSL later. Record pre-existing failures separately from new failures.

---

## 2. Product vision and technology

**GoServe — Secure File Transfer & Network Operations Dashboard.** Users sign in, manage only their own files, see upload progress and use an authenticated browser TCP console. Admins manage users and see safe system status.

| Layer | Choice |
|---|---|
| Backend | Go + standard `net/http` (use `http.CrossOriginProtection` or equivalent Origin/Fetch-Metadata checks where the Go version supports it) |
| API | Versioned REST `/api/v1` |
| Frontend | React + Vite + **plain JavaScript/JSX**, not TypeScript |
| Routing / state | React Router, TanStack Query |
| Frontend quality | ESLint (+ `jsx-a11y`), Vitest + React Testing Library; Playwright for Phase 9 smoke tests |
| Styling | CSS Modules or plain CSS |
| Database | PostgreSQL + `pgx` |
| Migrations | One tool only, chosen in Phase 3 (`golang-migrate` or `goose`); agent must justify the choice |
| Password hashing | Argon2id via `golang.org/x/crypto/argon2` (maintained), parameters configurable and aligned with current OWASP guidance |
| Rate limiting | `golang.org/x/time/rate` or small in-house limiter; keyed by IP and by username |
| Sessions | Opaque random token (>= 256 bits); store only its SHA-256 hash in DB |
| Browser realtime | WebSocket via a maintained library (for example `github.com/coder/websocket`); agent verifies maintenance status before adding |
| Logs / metrics | `log/slog`, Prometheus-compatible metrics |
| Vulnerability checks | `govulncheck`, `npm audit`, secret scanning in CI |
| TLS / proxy | Caddy or equivalent |
| Packaging / CI | Docker, Docker Compose, GitHub Actions |

Keep dependencies purposeful and explain each one. JavaScript means `.js`/`.jsx`; do not switch to TypeScript without approval. Do not hardcode dependency versions in prompts; let the agent pick current stable versions and record them.

### Target architecture
```text
Browser --HTTPS/WSS--> Caddy/reverse proxy --> GoServe
                                            |-- REST API /api/v1
                                            |-- WebSocket /ws/tcp
                                            |-- React production assets
                                            |-- PostgreSQL (users/sessions/file metadata)
                                            |-- Private file storage (not public static)
                                            `-- Shared TCP service logic (in-process)
```

Architecture rules:
- Prefer one public origin for frontend and API; avoid unnecessary CORS.
- Frontend never connects directly to PostgreSQL or raw TCP.
- Never expose filesystem paths, password hashes, session hashes or secrets to the browser.
- Uploads live outside the public static directory; never serve them with `http.FileServer`.
- DB stores metadata/ownership; file bytes stay in a storage layer behind an interface.
- TCP message processing is a shared in-process service used by the raw TCP and WebSocket adapters. The WebSocket gateway calls the service directly; it never dials a host/port.
- Raw TCP listener is **disabled by default** for public production. If enabled, it needs its own TLS/authentication/rate-limit design; HTTPS does not secure a separate TCP port.
- WebSocket validates session, Origin, authorization, message size and deadlines.

### Suggested target structure (migrate gradually)
```text
cmd/goserve/main.go
cmd/tcp-client/
internal/app/                 # dependency wiring/lifecycle
internal/config/              # YAML/env/CLI config
internal/http/{handlers,middleware,routes.go}
internal/auth/                # password/session/authorization
internal/users/               # user service/repository
internal/files/               # upload/list/download/delete
internal/storage/             # local storage adapter
internal/tcp/                 # protocol and lifecycle
internal/websocket/           # browser TCP gateway
internal/database/            # PostgreSQL/migrations
internal/observability/       # logs/metrics/health
migrations/
web/src/{api,app,components,features,hooks,layouts,pages,styles}/
web/{package.json,vite.config.js}
deploy/Caddyfile
docs/{PROGRESS.md,AUDIT.md,ARCHITECTURE.md,SECURITY.md,OPERATIONS.md,RELEASE_CHECKLIST.md}
Dockerfile, compose.yaml, .github/workflows/
```
Do not create empty packages just to match the tree. Avoid import cycles and preserve tests during migration.

---

## 3. Functional requirements

### Authentication / accounts
- Controlled account creation (admin-created/invite). No open registration unless explicitly chosen (section 11).
- Argon2id with per-password random salt; enforce a sensible minimum length (>= 12) and a maximum length (to avoid hashing-DoS); no composition rules; constant-time verification; compare against a dummy hash for unknown users so response time does not reveal valid usernames.
- Cryptographically random opaque sessions; DB stores only a token hash.
- Cookies: `HttpOnly`, `Secure` in production, appropriate `SameSite`, bounded expiry (idle + absolute); consider the `__Host-` prefix in production; rotate the session after login and privilege changes; revoke server-side on logout and on password change.
- CSRF protection for state-changing cookie-authenticated requests (SameSite + Origin/Fetch-Metadata check + token or custom-header strategy, documented).
- Login rate limiting (per IP and per username) with generic authentication errors.
- Roles at minimum `admin` and `user`; enforce authorization in the backend, not by hiding UI buttons.
- Safe bootstrap-admin command/one-time flow; no hardcoded default credentials. Prevent the last active admin from being disabled or demoted.
- Never log passwords, cookies, authorization headers, session tokens or secrets.

### File management
- Start with single-file upload; add multi-file only after single upload is robust.
- Stream uploads with total-body and per-file limits; enforce per-user quota and file-count limits atomically (inside a transaction or with row locking) so parallel uploads cannot exceed the quota.
- Never trust client filename or MIME type. Generate a server-side storage key; keep a sanitized display name separately. Reject Windows reserved names and control characters in display names.
- Temp file + atomic finalize (rename). A file row starts as `pending` and becomes `ready` only after storage and DB both succeed; `pending`/incomplete files are never listed or downloadable.
- Clean up if storage write or DB transaction fails; define orphan reconciliation (DB rows without bytes, bytes without rows).
- Prevent traversal, overwrite, symlink escapes and any client-supplied filesystem path.
- Paginated listing scoped to owner (admin can see all). Download by opaque file ID with `http.ServeContent` (Range support), safe `Content-Disposition`, `X-Content-Type-Options: nosniff`. Delete checks owner/admin.
- Consistent JSON errors; do not reveal whether another user's file exists (return 404, not 403, for foreign files).

### TCP / WebSocket
- Preserve and document current line framing and message-size limit.
- Extract message processing into a reusable service.
- Browser connects to authenticated `/ws/tcp`; validate Origin before upgrade.
- Enforce deadlines, message-size limits, per-user and global connection limits, idle timeout, ping/pong and graceful close.
- Gateway reaches only GoServe's internal service, never a user-supplied host/port.
- Raw TCP disabled by default for public production; if enabled: TLS + auth + rate limits + firewall/private-network policy. Do not call plain TCP "encrypted".
- Do not log raw message contents by default.

### Dashboard pages
1. Login.
2. Overview: real file count / storage / activity (no fake statistics).
3. My Files: paginated + searchable list, download/delete.
4. Upload: drag/drop or picker, server-provided limits, progress, cancellation where safe.
5. TCP Console: connect/disconnect, send/receive, connection state, bounded history.
6. Admin Users: create/disable users, roles, session revocation.
7. Admin Health: safe status and configured limits; never secrets.

Frontend must be responsive and accessible, with loading/error/empty/offline states, a centralized API client and no secrets in `localStorage`. Prefer HttpOnly cookie sessions. Frontend route protection is UX only; the backend is authoritative.

---

## 4. Target API and database

All JSON endpoints use `/api/v1`; document exact fields and test them. Do not serialize internal Go structs directly.

| Method | Endpoint | Purpose | Access |
|---|---|---|---|
| GET | `/api/v1/health/live` | Liveness | Public/internal |
| GET | `/api/v1/health/ready` | DB/storage readiness | Internal/controlled |
| POST | `/api/v1/auth/login` | Create session | Public + rate limit |
| POST | `/api/v1/auth/logout` | Revoke session | Auth + CSRF |
| GET | `/api/v1/auth/me` | Current user (+ CSRF token if that strategy is chosen) | Auth |
| GET | `/api/v1/limits` | Upload size/quota/count limits for the UI | Auth |
| GET | `/api/v1/files` | Paginated files (`limit`, `cursor`, `q`) | Auth, owner scope |
| POST | `/api/v1/files` | Upload | Auth + CSRF |
| GET | `/api/v1/files/{id}` | Metadata | Owner/admin |
| GET | `/api/v1/files/{id}/download` | Download (Range supported) | Owner/admin |
| DELETE | `/api/v1/files/{id}` | Delete | Owner/admin + CSRF |
| GET | `/api/v1/dashboard/summary` | Dashboard summary | Auth |
| GET/POST | `/api/v1/admin/users` | List/create users | Admin |
| PATCH | `/api/v1/admin/users/{id}` | Update role/active; revoke sessions | Admin + CSRF |
| GET | `/api/v1/admin/system` | Safe system status | Admin |
| GET | `/ws/tcp` | WebSocket console | Auth + Origin validation |

Use UTC RFC 3339 timestamps and stable machine-readable error codes. Never return stack traces or filesystem paths. Paginate with a bounded `limit` and cursor. Use correct status codes (400, 401, 403, 404, 409, 413, 415, 429, 500, 503).

Success example:
```json
{"data":{"id":"opaque-file-id","name":"report.pdf","size":24576,"contentType":"application/pdf","createdAt":"2026-10-31T10:00:00Z"}}
```
Error example:
```json
{"error":{"code":"FILE_TOO_LARGE","message":"The selected file exceeds the configured upload limit."}}
```

### Initial PostgreSQL tables
- **users:** UUID id, normalized unique username, optional unique email, password_hash, role, is_active, optional quota override, timestamps.
- **sessions:** UUID id, user_id FK, unique token_hash, expires_at, created_at, last_seen_at, optional revoked_at.
- **files:** UUID id, owner_id FK, unique server-generated storage_key, sanitized original_name, size_bytes, server-detected content_type, optional sha256, status (`pending`/`ready`), created_at.
- **audit_events:** id, actor_user_id, event_type, resource_type/id, timestamp, safe metadata only (never secrets, never file contents).

Use versioned migrations, constraints, foreign keys and indexes (for example owner_id + created_at for listing). Do not store file bytes in PostgreSQL. No destructive auto-migrations; every migration documents its rollback.

---

## 5. Security and operations release gates

### Security
- [ ] No hardcoded secrets, credentials, private keys or tokens.
- [ ] Argon2id passwords; no plaintext/reversible password storage; user-enumeration timing tested.
- [ ] Random, revocable, rotated sessions; only token hashes stored.
- [ ] Cookie flags and CSRF protection tested.
- [ ] Backend authorization on every protected resource.
- [ ] Login/API rate limits.
- [ ] Streaming upload limits and atomic quotas.
- [ ] User A cannot list/download/delete User B files (tested).
- [ ] Traversal, malicious filename, duplicate, partial-write and symlink tests.
- [ ] WebSocket Origin/auth/message/deadline/connection limits.
- [ ] Raw TCP disabled by default or secured with TLS + auth.
- [ ] Same-origin CORS disabled unless a strict allowlist is required.
- [ ] Security headers/CSP; public errors do not leak internals; logs redact secrets.
- [ ] Dependency and vulnerability checks (`govulncheck`, `npm audit`) and secret scanning in CI.

### Operations
- [ ] Trusted production TLS certificate; never use `curl -k` as production validation.
- [ ] Secrets injected at runtime (env or secret files), not committed YAML or Docker layers.
- [ ] Non-root minimal container and health checks.
- [ ] DB backup **and restore** procedure tested.
- [ ] Upload persistence/backup strategy documented.
- [ ] Structured logs, request IDs, metrics, health/readiness.
- [ ] Graceful shutdown covers HTTP, WebSocket, TCP, DB and workers.
- [ ] Production config validation and bounded resource limits.
- [ ] Deployment rollback and migration strategy documented.

---

## 6. Agent working rules (both tools)

1. Open GoServe in your IDE; use Agent mode (Copilot) or Planning behavior (Antigravity).
2. Paste **only one phase prompt at a time**.
3. The agent inspects current files/tests before editing and presents a short plan for non-trivial phases.
4. Review diffs; reject broad rewrites that the phase does not need.
5. Run checks and read the actual output yourself.
6. **You** commit, after acceptance criteria pass. The agent never commits or pushes.
7. Move to the next phase only when the previous one is stable, merged and logged in `docs/PROGRESS.md`.

`AGENTS.md` already contains the global rules, so the phase prompts below start with one reference line instead of repeating them. If your tool does not load `AGENTS.md`, paste this block before the phase prompt:

```text
Work in the existing GoServe repository. Read AGENTS.md, README.md,
PRODUCTION_ROADMAP.md and docs/PROGRESS.md and inspect relevant source/tests
before editing. Preserve current behavior unless the phase explicitly changes
it. Do not invent test results, delete tests to make a build pass, add fake API
success data, TODO-only stubs or hardcoded secrets. Keep changes small,
explain important design decisions, update docs and provide manual
verification. Do not commit or push; wait for my review. At the end list
changed files, design summary, exact commands/results (PASS/FAIL/NOT RUN),
known risks, manual checks and a proposed commit message. Explain changes in
beginner-friendly language.
```

---

## 7. Phase-wise implementation prompts

Each prompt is self-contained. Suggested branch names are in brackets.

### Phase 0 — Audit and baseline  [`phase-0-audit`]
```text
Follow AGENTS.md. Read README.md and PRODUCTION_ROADMAP.md completely.
Inspect the entire GoServe repository, all Go source and tests.
Do not modify existing files. You may create only docs/AUDIT.md and
docs/PROGRESS.md (use the template in the roadmap).

1. Explain the current request flow for HTTPS static serving, upload, TCP
   server/client, configuration and graceful shutdown.
2. Identify coupling, security risks, lifecycle risks and test gaps from the
   actual source.
3. Verify each finding F1-F10 in roadmap section 1 against the real code:
   confirm, refute or refine it with file/function evidence. Add any new
   findings you discover.
4. Run go version, go test ./..., go vet ./..., go test -race ./... if
   possible (say NOT RUN and why if not). Report exact output.
5. Compare findings against the roadmap and say whether any phase should be
   reordered or split.
6. Produce a prioritized audit with severity, file/function evidence,
   recommended fix, target phase and production impact.

No code changes. Wait for my approval before Phase 1.
```
**Acceptance:** baseline and pre-existing failures recorded; no source changed; F1-F10 verified.
**Commit:** `docs: add GoServe production readiness audit`

### Phase 1 — Safe backend refactoring + minimal CI  [`phase-1-refactor`]
```text
Follow AGENTS.md. Implement Phase 1 only: behavior-preserving backend
refactoring plus a minimal CI workflow. Read docs/AUDIT.md first and propose
the migration sequence before editing; wait for my OK.

Move startup into cmd/goserve and introduce only the necessary internal
packages for config, app lifecycle, HTTP handlers/middleware and TCP
protocol/server. Keep cmd/tcp-client working. Preserve HTTPS static serving,
upload behavior, YAML/CLI precedence, TCP framing/limits and graceful
shutdown. Keep/adapt all tests. Use dependency injection where it improves
testing. Avoid import cycles and global mutable state. Preserve CLI flags and
defaults unless documented. This phase is a pure refactor: do not fix
findings, add DB, React or new features yet.

Add .github/workflows/ci.yml that runs on push/PR: gofmt check, go vet,
go test, go test -race (Linux runner). Keep it small; Docker and frontend
jobs come later. Update README with the new layout and run command.

Run gofmt, go test ./..., go vet ./..., go test -race ./... and report exact
results. Do not delete tests to make the build pass.
```
**Acceptance:** builds from `cmd/goserve`; existing behavior/tests unchanged; package responsibilities clear; CI file valid.
**Commit:** `refactor: organize GoServe backend into internal packages and add CI`

### Phase 1B — Low-risk hardening of existing behavior  [`phase-1b-hardening`]
```text
Follow AGENTS.md. Implement Phase 1B only: small hardening fixes to the
EXISTING server, each with a test, each documented as an intentional
behavior change. Do not add new features, DB or frontend.

1. Compare upload Basic Auth credentials with crypto/subtle constant-time
   comparison and add a simple per-IP failed-auth limiter (temporary
   stopgap until Phase 4).
2. Lower the in-memory part of multipart parsing (for example 1 MiB) so large
   uploads spill to disk, while keeping the total request-body limit.
3. Add MaxHeaderBytes and per-request body/write deadlines for upload using
   http.ResponseController instead of a global WriteTimeout that would break
   large uploads.
4. Static serving: disable directory listings and do not serve dotfiles.
5. Make TLS cert/key paths configurable (config + flags + example YAML),
   defaulting to the current cert/server.crt and cert/server.key.
6. Upload files: mode 0600, bounded unique-name retries, reject Windows
   reserved names.
7. Access log records response status code (still no paths/headers/bodies).
8. README cleanup: remove personal absolute paths; move test.txt to testdata/
   or document why it stays.

Preserve YAML/CLI precedence and all existing flags. Run gofmt, vet, tests
and race (or NOT RUN) and report exact results. List every behavior change.
```
**Acceptance:** findings F1-F3 (stopgap), F5-F9 resolved or consciously deferred; all tests green; behavior changes documented.
**Commit:** `fix(security): harden existing HTTP, upload and static serving behavior`

### Phase 2 — React JavaScript foundation  [`phase-2-web`]
```text
Follow AGENTS.md. Implement Phase 2 only: React + Vite frontend using plain
JavaScript/JSX, not TypeScript.

Create web/ with React, Vite, React Router, TanStack Query, ESLint (with
jsx-a11y) and Vitest + React Testing Library. Build responsive shells for
Login, Dashboard, My Files, Upload, TCP Console and Admin. Add accessible
reusable UI components only where justified. Centralize the API client for
same-origin cookie sessions. Configure the Vite dev proxy for /api and /ws.

Do not invent backend endpoints or fake successful login/upload data.
Unconnected pages must clearly say "not connected yet". Add loading/error/
empty states. No secrets in frontend env. Document how the React build is
served under the same origin. If using Go embed, make sure the embed
directory exists in a clean clone and the build works from scratch. Add
npm scripts dev/build/lint/test and commit the lockfile. Extend CI with a
frontend job (npm ci, lint, test, build).

Run npm ci (or npm install to create the lockfile), npm run lint, npm test
and npm run build; report exact output. If a browser tool is available,
open the dev server and check keyboard navigation and a narrow mobile width;
otherwise mark that check NOT RUN and give manual steps.
```
**Acceptance:** dev server and production build work; no fake API flows; responsive accessible shell; frontend tests run in CI.
**Commit:** `feat(web): add React JavaScript dashboard foundation`

### Phase 3 — PostgreSQL and persistence  [`phase-3-database`]
```text
Follow AGENTS.md. Implement Phase 3 only: PostgreSQL integration and
migrations.

Add pgx, a DSN configurable via environment/secret file, pool config, startup
timeout, versioned SQL migrations for users/sessions/files/audit_events
(files include a status column pending/ready), repository interfaces and
implementations, context-aware queries, transactions, a readiness check and
graceful DB close. Choose ONE migration tool and justify it before adding it.

Use UUIDs, constraints, foreign keys and indexes. Do not store file bytes in
PostgreSQL. Do not expose DB models as API JSON. Add repository tests and
integration tests against real PostgreSQL (Docker Compose locally, a service
container in CI). Avoid destructive automatic migrations; document the
rollback for each. Document local DB setup. Do not implement login or
frontend API integration yet.

Run Go tests/vet/race and state exactly which tests used a real DB. If Docker
is not available, mark integration tests NOT RUN and explain how I can run them.
```
**Acceptance:** clean DB migrates from zero; repositories work; DB failure handled clearly; CI runs the integration tests.
**Commit:** `feat(database): add PostgreSQL repositories and migrations`

### Phase 4 — Authentication, sessions and roles  [`phase-4-auth`]
```text
Follow AGENTS.md. Implement Phase 4 only: production-oriented
authentication/authorization using the Phase 3 repositories.

Implement admin-created accounts (no open registration by default), Argon2id
hashing (configurable parameters, min/max password length, dummy-hash check
for unknown users), login/logout/me endpoints, random opaque session tokens
with only SHA-256 hashes in the DB, HttpOnly/Secure-in-production/SameSite
cookies, idle + absolute expiry, revocation, session rotation, CSRF
protection (document the strategy), login rate limiting per IP and per
username, and generic auth errors.

Implement admin/user roles enforced in backend services; block disabling or
demoting the last active admin. Add a safe bootstrap-admin command/one-time
flow with no default credentials. Audit login/logout/user/role events without
secrets. Connect the React Login page, current-user state, logout and
expired-session handling. Frontend route protection is UX only.

Test: wrong password, unknown user (timing and message), expired/revoked
session, CSRF, cookie flags, disabled user, role restrictions, rate limit and
rotation. Do not keep Basic Auth as the primary user auth; any temporary
migration mode must be explicit, disabled by default and documented.
```
**Acceptance:** no plaintext passwords/raw session tokens in DB/logs; auth/CSRF/role tests pass; last-admin protection tested.
**Commit:** `feat(auth): add secure user sessions and role-based access`

### Phase 5 — File management and UI integration  [`phase-5-files`]
```text
Follow AGENTS.md. Implement Phase 5 only: secure file management. Refactor
the current upload into a file service and a storage interface; review and
preserve existing protections.

Implement authenticated /api/v1/files upload with streaming total and
per-file limits, server-generated storage keys, sanitized display names,
temp file + atomic finalize, files.status pending -> ready, cleanup after
disk/DB failure, server-side MIME validation policy, per-user quota and file
count enforced atomically, paginated list/search, metadata, authorized
download (http.ServeContent with Range) and delete. Add GET /api/v1/limits.

Enforce owner/admin checks on every operation and return 404 for foreign
files. Add safe Content-Disposition, X-Content-Type-Options: nosniff, storage
outside public assets, traversal/symlink protection and an orphan
reconciliation strategy. Retire the legacy /upload route (document it).
Connect My Files and Upload pages to the real APIs: real progress, safe
cancellation, metadata, errors, empty state, download and delete confirmation.
No fake data.

Test: User A cannot list/download/delete User B files; oversized, malformed,
duplicate, malicious-filename, traversal, symlink, partial-write and
parallel-quota cases; DB/storage failure cleanup; pagination; safe
Content-Disposition. Never accept arbitrary filesystem paths from the client.
```
**Acceptance:** upload/list/download/delete works with real DB/storage; ownership isolation proven; parallel uploads cannot exceed quota.
**Commit:** `feat(files): add secure file management and dashboard integration`

### Phase 6 — TCP hardening and WebSocket console  [`phase-6-tcp-ws`]
```text
Follow AGENTS.md. Implement Phase 6 only: TCP hardening and an authenticated
WebSocket gateway.

Extract the existing TCP message processing into a reusable in-process
service used by both raw TCP and WebSocket. Preserve/document framing and max
size. Add context cancellation, deadlines, a global max-connections limit
with a semaphore, per-connection idle timeout and graceful close. Make the
raw TCP listener disabled by default and bind to localhost unless configured.

Add /ws/tcp requiring an authenticated session before upgrade, strict Origin
validation, message limits, deadlines, ping/pong, per-user connection limits
and close handling. The gateway calls the service directly; it must not dial
any host/port and must not accept one from the client. Do not log raw message
contents. Connect the TCP Console UI with connect/disconnect, send/response,
state, reconnect and bounded history.

If the raw TCP listener is enabled, require documented TLS + authentication +
rate limits + firewall/private-network guidance. Never call plain TCP
encrypted. Test: unauthorized WS, invalid Origin, oversized messages, idle
clients, concurrency, disconnect, shutdown, connection-limit rejection and
protocol parity between raw TCP and WS.
```
**Acceptance:** browser console works through authenticated WSS; no arbitrary TCP proxying; limits/shutdown tested.
**Commit:** `feat(tcp): add secure WebSocket console and TCP hardening`

### Phase 7 — Security and observability  [`phase-7-ops`]
```text
Follow AGENTS.md. Implement Phase 7 only: production security and
observability.

Add structured slog logs, request IDs, safe access logs (method, route
pattern, status, duration) and redaction of Authorization/Cookie/password/
session tokens/secrets/upload contents. Add Prometheus-compatible request and
latency, upload, active connection, WebSocket and DB pool metrics. Add
liveness/readiness. Add security headers and a CSP compatible with the
frontend. Keep CORS disabled unless a strict allowlist is needed. Add
rate/resource limits, production config validation (refuse to start with
insecure production settings), safe error mapping and graceful shutdown for
HTTP, WebSocket, TCP, workers and DB. Add govulncheck to CI.

Test: redaction, headers, readiness, rate limits, timeouts and shutdown. Do
not expose metrics publicly without an explicit decision from me; default to
an internal-only listener or authenticated route.
```
**Acceptance:** useful safe logs/metrics/health; bounded resources; no secret leakage; govulncheck in CI.
**Commit:** `feat(ops): add observability and production security controls`

### Phase 8 — Docker, reverse proxy and CI  [`phase-8-deploy`]
```text
Follow AGENTS.md. Implement Phase 8 only: deployment, containers and CI.

Add a multi-stage Dockerfile building React and Go with a minimal runtime
image and a non-root user, .dockerignore, and compose.yaml for GoServe +
PostgreSQL + Caddy with persistent DB/upload volumes, a Caddy HTTPS reverse
proxy with WebSocket support, health checks and runtime secrets/environment.
Extend GitHub Actions: Go format/vet/test/race, frontend install/lint/test/
build, govulncheck, npm audit, Docker build and secret scanning; add
Dependabot config.

Do not expose PostgreSQL or the raw TCP port publicly; publish only the
reverse-proxy ports. Never bake secrets into image layers. Document
domain/DNS, TLS, firewall, migrations, backup/restore, logs, metrics,
upgrade and rollback. Keep local and production instructions separate. Do not
claim a deployment exists unless it was tested.

Run docker compose config and the build/tests if Docker is available;
otherwise mark NOT RUN and explain.
```
**Acceptance:** clean checkout builds; DB/files persist; only intended ports exposed; CI validates both stacks.
**Commit:** `build(deploy): add secure Docker Compose and CI pipeline`

### Phase 9 — End-to-end release validation  [`phase-9-release`]
```text
Follow AGENTS.md. Implement Phase 9 only: final verification, not major
features.

Create a verification matrix and test: fresh DB migration/admin bootstrap,
login/logout/session expiry/CSRF, User A vs User B isolation, upload
progress/limits/quota/duplicate/download/delete, authenticated WebSocket
console, invalid Origin/oversized WS rejection, health/readiness/metrics,
graceful shutdown with active connections, restart persistence, Docker
startup, frontend production build + a Playwright smoke test, and backup +
restore.

Run applicable checks: gofmt, go test ./..., go test -race ./..., go vet
./..., govulncheck, npm ci, npm run lint, npm test, npm run build, docker
compose config and image build. Do not fabricate browser/load/security/backup
results. Mark environment-dependent checks NOT RUN and give manual steps.

Create docs/RELEASE_CHECKLIST.md, docs/SECURITY.md, docs/OPERATIONS.md and
docs/ARCHITECTURE.md. Update the README with architecture, setup, API,
deployment, limitations and screenshots (only real screenshots). Report
PASS/FAIL/NOT RUN for every gate. Do not call GoServe production-ready while
a critical gate is FAIL or NOT RUN. List the open LICENSE decision.
```
**Acceptance:** honest verification matrix; critical gates pass; operations/rollback documented.
**Commit:** `docs(release): add GoServe production verification and operations guide`

---

## 8. UI direction

Keep the UI clean and functional; do not prioritize animation over working features.

```text
┌──────────────────────────────────────────────────────────┐
│ GoServe                                  User / Logout   │
├───────────────┬──────────────────────────────────────────┤
│ Overview      │  Welcome back                            │
│ My Files      │  [Files] [Storage] [Activity]            │
│ Upload        │                                          │
│ TCP Console   │  Recent files / recent activity          │
│ Admin         │                                          │
└───────────────┴──────────────────────────────────────────┘
```

- **My Files:** search, pagination, name/type/size/date, download/delete; never show server paths.
- **Upload:** drag/drop, server-provided size limit, progress, cancel; success only after the server confirms persistence.
- **TCP Console:** connection state, reconnect, message input and bounded history; clearly a GoServe protocol console, not an arbitrary terminal.
- **Accessibility:** keyboard navigable, visible focus, labelled inputs, sufficient contrast, status messages announced to screen readers.

## 9. Definition of Done (every phase)

- [ ] Clear package responsibility; existing behavior preserved or migration documented.
- [ ] Unit tests for new behavior; negative tests for security-sensitive behavior.
- [ ] Integration tests cover DB/storage/network boundaries where applicable.
- [ ] `gofmt`, `go test ./...`, `go vet ./...` pass.
- [ ] `go test -race ./...` run and result recorded (or `NOT RUN` with reason; CI covers it).
- [ ] Frontend lint/test/build pass for frontend phases.
- [ ] README and example config updated.
- [ ] No secrets/runtime data committed (`git status` checked).
- [ ] Manual verification documented; actual command output recorded.
- [ ] `docs/PROGRESS.md` updated.
- [ ] Diff reviewed by me before commit.

## 10. Useful commands (PowerShell)

Backend:
```powershell
gofmt -l .          # lists unformatted files; should print nothing
gofmt -w <files>    # format only what you changed, then inspect the diff
go test ./...
go test -v ./...
go test -race ./...  # needs cgo + a C compiler on Windows; otherwise use CI/WSL
go vet ./...
go build ./cmd/goserve
```
Frontend:
```powershell
cd web
npm ci
npm run dev
npm run lint
npm test
npm run build
```
Docker:
```powershell
docker compose config
docker compose build
docker compose up -d
docker compose ps
docker compose logs -f
```
Run commands only after the relevant phase has created the paths/scripts. Do not run `gofmt -w .` blindly if it could touch generated or vendored code; inspect the diff afterwards.

## 11. Decisions required (the agent must not guess)

Fill the "My decision" column before the phase that needs it. The recommended defaults are safe for development and learning.

| # | Question | Needed by | Recommended default | My decision |
|---|---|---|---|---|
| 1 | Deployment target: VPS, cloud VM, university server or local LAN? | Phase 8 | Local Docker Compose first, VPS later | |
| 2 | Access model: private users only, or public registration? | Phase 4 | Admin-created accounts only | |
| 3 | File storage: local persistent disk or S3-compatible? | Phase 5 | Local disk behind a storage interface | |
| 4 | TCP access: authenticated WebSocket only, or raw TCP client too? | Phase 6 | WebSocket only; raw TCP off / localhost | |
| 5 | Production domain/subdomain for HTTPS? | Phase 8 | Use `localhost` until a domain exists | |
| 6 | Session/CSRF strategy (cookie + header token vs. SameSite + Origin checks) | Phase 4 | Cookie session + Origin check + CSRF token header | |
| 7 | Default per-user quota and max file size | Phase 5 | 100 MiB per file, 1 GiB per user (configurable) | |
| 8 | LICENSE (MIT, Apache-2.0 or none) | Phase 9 | MIT if you want it public on your resume | |

## 12. First instruction — start here

Paste this first in **either** tool. It intentionally asks for Phase 0 only:

```text
Treat PRODUCTION_ROADMAP.md and AGENTS.md as the GoServe execution plan.
Start with Phase 0 only. Do not implement multiple phases in one go. Inspect
the repository, explain the current architecture, run baseline checks and
report exact results. You may create only docs/AUDIT.md and docs/PROGRESS.md;
do not change code yet. Wait for my approval before Phase 1. After each
approved phase, implement only that phase, run its checks, report actual
results and suggest the commit message. Preserve the existing project and
explain important changes in beginner-friendly language.
```

## 13. When things go wrong

- **Agent started the next phase or rewrote everything:** stop it, `git restore .` (or discard the branch), and re-paste only the current phase prompt plus: `You went beyond the phase. Re-read AGENTS.md phase discipline and redo only Phase N.`
- **Agent lost context / you switched tools:** use the handoff prompt in section 0.
- **Tests fail:** ask for the exact failing output, a root-cause explanation and the smallest fix. Never accept "deleted the failing test".
- **Agent claims success without output:** reply `Show the exact command output. Mark anything you did not run as NOT RUN.`
- **`-race` or Docker unavailable locally:** accept `NOT RUN` locally, make sure CI covers it, and re-check at Phase 9.
- **Phase too big for one session:** ask the agent to split it into 2-3 sub-steps (for example Phase 5a storage + service, 5b API, 5c UI) and approve each separately.

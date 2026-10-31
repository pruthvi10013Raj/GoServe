# AGENTS.md — GoServe working rules for AI coding agents

> Read by VS Code Copilot (Agent mode) and Google Antigravity. Keep this file short
> (Antigravity limits rule files to 12,000 characters). The detailed plan lives in
> `PRODUCTION_ROADMAP.md`; this file only holds the rules that apply to every task.

## Project
GoServe is a Go project being upgraded from a learning-level file server into a
secure file-transfer and TCP-console web app. Plan: `PRODUCTION_ROADMAP.md`.
Progress and handoff notes: `docs/PROGRESS.md`. Humans may switch between
different AI tools between phases, so these files are the shared memory.

## Stack (do not change without asking)
- Backend: Go, standard `net/http`, `log/slog`, PostgreSQL via `pgx`.
- Frontend: React + Vite, **plain JavaScript/JSX (no TypeScript)**, React Router,
  TanStack Query, plain CSS or CSS Modules.
- Deploy: Docker, Docker Compose, Caddy, GitHub Actions.
- Dev machine is Windows (PowerShell). Give PowerShell-friendly commands.

## Phase discipline (most important)
1. Work on **one phase at a time**, the one the user names. Never start the next phase.
2. Before editing: read `README.md`, the relevant roadmap section, `docs/PROGRESS.md`,
   and the source/tests you will touch.
3. For non-trivial phases, first present a short plan (files to change, order, risks)
   and wait for the user's go-ahead.
4. Keep changes small and reviewable. No broad rewrites, no drive-by refactors.
5. Never run `git commit`, `git push`, `git reset --hard`, or delete branches.
   The user commits after reviewing. Propose a commit message instead.
6. Ask the user before: adding a dependency not listed in the roadmap, changing the
   Go version, changing the public API shape, or making any decision listed in
   roadmap section 11 ("Decisions required").

## Honesty rules
- Run the checks yourself and paste **actual output**. If a command cannot run
  (no Docker, no gcc for `-race`, no browser), write `NOT RUN` and explain why.
- Never claim something passes unless you ran it. Never fake API data, test results,
  screenshots, benchmarks or "TODO-only" stubs presented as finished work.
- Never delete or weaken a test to make a build pass. If a test is wrong, explain why
  and ask first.
- Record pre-existing failures separately from failures you caused.

## Security non-negotiables
- No hardcoded secrets, passwords, tokens, keys or default credentials. Secrets come
  from environment variables or runtime secret files, never from committed YAML,
  Docker layers or the frontend.
- Never log passwords, cookies, Authorization headers, session tokens, secrets or
  uploaded file contents.
- Authorization is enforced in the backend for every protected resource.
  Frontend route guards are UX only.
- Never trust client filename, MIME type, path, size header or Origin without validation.
- Uploaded files are never served through a public static directory.
- Do not expose PostgreSQL, metrics, or the raw TCP listener to the public internet.
- Do not claim the project is "production-ready" unless every critical gate in
  roadmap section 5 is PASS.

## Code conventions
- Go: `gofmt`, `go vet`, table-driven tests, `context.Context` first parameter,
  wrap errors with `%w`, no global mutable state, no import cycles, small interfaces
  defined where they are used. New packages only when they have a real responsibility.
- Public JSON uses `/api/v1`, stable error codes, UTC RFC 3339 timestamps. Never
  serialize DB/internal structs directly.
- Frontend: functional components, one centralized API client, loading / error /
  empty states on every data view, accessible labels, no secrets in `localStorage`.
- Comments explain *why*. Explain important changes in beginner-friendly language.

## Commands (run only when the phase has created the path)
```powershell
gofmt -l .                # must print nothing
go vet ./...
go test ./...
go test -race ./...       # needs cgo/gcc on Windows; if unavailable, say so and rely on CI/WSL
cd web; npm ci; npm run lint; npm test; npm run build
docker compose config
```

## Definition of done for a phase
- [ ] Phase acceptance criteria in the roadmap are met.
- [ ] Format, vet, tests (and race where supported) pass, with real output shown.
- [ ] New behavior has tests; security-sensitive behavior has negative tests.
- [ ] README / `config.example.yaml` / docs updated where behavior changed.
- [ ] No secrets or runtime data (uploads, certs, `.env`) staged. Check `git status`.
- [ ] `docs/PROGRESS.md` updated (see the template in the roadmap).

## Final report format (end of every task)
1. Summary of what changed and why (beginner-friendly).
2. Changed files list.
3. Exact commands run + real results (PASS / FAIL / NOT RUN).
4. Known risks and open questions.
5. Manual checks the user should do.
6. Proposed commit message (do not commit).

# GoServe progress log

## Current status
- Active phase: 1 — Safe backend refactoring + minimal CI (complete; awaiting review before Phase 1B)
- Branch: `phase-0-audit`
- Last tool used: Copilot
- Go version / Node version / Docker available: Go 1.27.0; Node v24.19.0; Docker 28.3.2

## Decisions made (see roadmap section 11)
| Date | Decision | Reason |
|---|---|---|
| — | No product decisions made | Phases 0 and 1 did not require roadmap section 11 decisions |

## Phase log
### Phase 0 — DONE
- Changed: created `docs/AUDIT.md` and `docs/PROGRESS.md`; no existing files or application source changed.
- Checks:
  - `go version` — PASS: `go version go1.27.0 windows/amd64`
  - `go test ./...` — PASS: `ok  	goserve	(cached)`; `ok  	goserve/cmd/tcp-client	(cached)`
  - `go vet ./...` — PASS: no output, exit code 0
  - `go test -race ./...` — PASS: `ok  	goserve	(cached)`; `ok  	goserve/cmd/tcp-client	(cached)`
  - Frontend and Docker checks — NOT RUN; Phase 0 requests Go baseline checks only and the repository has no frontend or Compose setup.
- Pre-existing failures: none in the requested checks. Before this audit, the worktree already had a modified `.gitignore` and untracked `public/ack.txt` plus a language-server-named file; these were preserved.
- Known risks / follow-ups: See prioritized findings F1-F11 in `docs/AUDIT.md`. Add static/upload directory separation to Phase 1B. No phase reordering or splitting recommended. Do not begin Phase 1 until the user approves.
- Commit: proposed `docs: add GoServe production readiness audit` (not committed; user commits after review).

### Phase 1 — DONE
- Changed: moved configuration to `internal/config`; extracted HTTP routes, upload handling, and middleware to `internal/httpserver`; extracted TCP protocol/server lifecycle to `internal/tcp`; added lifecycle wiring in `internal/app` and the executable at `cmd/goserve`. Moved and adapted all GoServe server/config tests into their owning packages without removing tests. Added `.github/workflows/ci.yml` and updated the README layout/run commands.
- Checks:
  - `gofmt -l .` — PASS: no output.
  - `go vet ./...` — PASS: no output; exit code 0.
  - `go test ./...` — PASS:
    ```text
    ?   	goserve/cmd/goserve	[no test files]
    ok  	goserve/cmd/tcp-client	(cached)
    ok  	goserve/internal/app	(cached)
    ok  	goserve/internal/config	(cached)
    ok  	goserve/internal/httpserver	(cached)
    ok  	goserve/internal/tcp	(cached)
    ```
  - `go test -race ./...` — PASS:
    ```text
    ?   	goserve/cmd/goserve	[no test files]
    ok  	goserve/cmd/tcp-client	(cached)
    ok  	goserve/internal/app	4.455s
    ok  	goserve/internal/config	1.989s
    ok  	goserve/internal/httpserver	2.310s
    ok  	goserve/internal/tcp	1.800s
    ```
  - `go build ./cmd/goserve ./cmd/tcp-client` — PASS: exit code 0; no output.
  - Live smoke: started `go run ./cmd/goserve -config config.example.yaml` with temporary process-only upload credentials. `curl.exe -k -i https://localhost:8443/hello.txt` returned HTTP 200 and `Hello from GoServe static file server!`. `go run ./cmd/tcp-client "hello GoServe"` initially failed because `localhost` resolved to `::1` while this Windows listener accepted connections via IPv4; rerunning `go run ./cmd/tcp-client -addr 127.0.0.1:9090 "hello GoServe"` returned `Received: hello GoServe`. Server was stopped after the smoke check.
  - CI workflow itself — NOT RUN locally; it is intended to run on GitHub Actions' Ubuntu runner.
- Pre-existing failures: none in the requested Go checks. The default TCP-client smoke attempt over IPv6 failed as noted above; the explicit IPv4 smoke check passed. The `.gitignore` edit and pre-existing untracked files from Phase 0 were preserved.
- Known risks / follow-ups: Phase 1 is a behavior-preserving refactor only; audit findings remain for Phase 1B and later phases. The Windows localhost/IPv6 smoke behavior should be considered when manually testing the default TCP client.
- Commit: proposed `refactor: organize GoServe backend into internal packages and add CI` (not committed; user commits after review).

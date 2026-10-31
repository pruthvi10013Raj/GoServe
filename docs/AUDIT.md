# GoServe Phase 0 Audit

**Audit date:** 2026-10-31  
**Branch:** `phase-0-audit`  
**Scope:** Repository source, tests, configuration, documentation, and baseline Go checks. No application code was changed.

## Executive summary

GoServe is a small, single-process Go application. It has a useful baseline of
tests around configuration, upload handling, TCP framing, and shutdown, and the
requested Go checks pass on the current Windows toolchain. It is not ready to
be exposed as a production service: its raw TCP listener is unauthenticated,
unencrypted, and bound on all interfaces by default; upload request parsing
and HTTP request bodies have resource-control gaps; and public static serving
can disclose uploaded files if the configured upload directory overlaps the
static directory.

The roadmap's F1-F10 are substantially confirmed. F11 below is an additional
configuration-dependent exposure found in the implementation. Keep the
existing phase order, but add an explicit static/upload directory separation
check to Phase 1B and retain private storage as a Phase 5 requirement.

## Current request and lifecycle flows

### Configuration and startup

`parseServerConfig` in [`config.go`](../config.go#L166) loads built-in defaults,
then overlays a single YAML document with unknown-field rejection, applies only
explicit CLI overrides, and validates the resulting settings. Legacy `-port`
and `-dir` aliases are preserved; conflicting alias/new-flag pairs are rejected.
The YAML fields cover HTTPS/TCP ports, static/upload directories, and the
maximum upload request size. Upload credentials are read from
`GOSERVE_UPLOAD_USERNAME` and `GOSERVE_UPLOAD_PASSWORD` by
`uploadCredentialsFromEnvironment` in [`main.go`](../main.go#L31); startup
fails if either is blank.

`runWithHTTPS` in [`main.go`](../main.go#L389) starts TCP on `:<configured port>`
first, constructs the HTTP server, then runs HTTPS in a goroutine. TLS files
are fixed relative paths, `cert/server.crt` and `cert/server.key`, rather than
configuration values. Both server listeners therefore bind all interfaces by
default. A TLS startup error is reported and the TCP listener is shut down.

### HTTPS static serving

`newConfiguredHTTPHandler` in [`main.go`](../main.go#L213) registers
`http.FileServer(http.Dir(config.StaticDirectory))` at `/`. The file server
serves public content and uses Go's normal directory/index behavior; the
application adds no dotfile filtering or directory-listing suppression.
`loggingMiddleware` records the method, fixed route label (`/`), and elapsed
time, not the requested path, query, headers, or body.

### Upload

The `/upload` handler is wrapped in Basic Authentication and route logging.
`basicAuth` in [`main.go`](../main.go#L47) compares username and password with
ordinary string inequality and has no rate limiter. After authentication,
`uploadHandlerWithConfig` in [`main.go`](../main.go#L145) accepts POST only,
wraps the body in `http.MaxBytesReader`, parses multipart data with
`ParseMultipartForm(config.MaxUploadSize)`, removes multipart temporary files,
and reads the first `file` field.

The handler sanitizes the client filename, creates the configured upload
directory, and calls `saveUploadedFile`. `createUniqueUploadFile` uses
`O_CREATE|O_EXCL` to avoid overwriting an existing file, then tries successive
suffixes until a name is available. The file mode requested is `0666` (subject
to platform and process permissions). On copy or close errors the partial
file is removed. A successful response includes the resulting filename.
Although the default upload directory is `./uploads`, configuration does not
prevent it from being the static directory or a directory inside it.

### TCP server and client

`startTCPServer` in [`main.go`](../main.go#L298) listens on the supplied
address, which startup formats as `:<port>`. `acceptConnections` tracks each
connection and starts one goroutine per accepted client without a connection
limit. `handleTCPConnectionWithTimeout` reads one LF-terminated message,
accepts CRLF, enforces a 1024-byte message limit, and applies a five-second
read and write deadline. It replies `Received: <message>` or an oversized
request error, then closes the connection.

The standalone client in [`cmd/tcp-client/main.go`](../cmd/tcp-client/main.go#L19)
rejects multiline and oversized messages, uses a five-second connection and
I/O deadline, sends one line, and reads a bounded newline-terminated response.
The protocol is raw TCP: it has no authentication or TLS.

### Shutdown

`runWithSignals` in [`main.go`](../main.go#L437) creates a context canceled by
Ctrl+C or SIGTERM. On cancellation, `runWithHTTPS` shuts down TCP first:
the listener and all active connections are closed, and accept/handler
goroutines are awaited. It then calls `shutdownHTTPServer`, which gives active
HTTP requests up to ten seconds before forcing closure. If HTTPS exits first,
the TCP server is also stopped. TCP shutdown is an immediate connection close,
not a graceful completion of in-flight protocol exchanges.

## Findings and roadmap verification

Severity reflects the implementation and exposure conditions, not just the
fact that a feature is absent. “Production impact” describes the consequence
if the current service is deployed without the recommended mitigation.

| # | Severity | Status and evidence | Recommended fix | Target phase | Production impact |
|---|---|---|---|---|---|
| F1 | Medium | **Confirmed.** [`basicAuth`](../main.go#L47) uses `username != ...` and `password != ...`; no failed-auth limiter is present. | Use constant-time credential comparison and a bounded per-IP failed-auth limiter as a stopgap; replace the shared credential model with Phase 4 authentication and rate limits. | Phase 1B; replaced in Phase 4 | Online password guessing is not throttled. Timing behavior may also disclose comparison progress; HTTPS does not remove server-side timing differences. |
| F2 | Medium | **Confirmed, with qualification.** [`uploadHandlerWithConfig`](../main.go#L145) passes the full configured request limit (10 MiB by default) as `ParseMultipartForm`'s in-memory threshold, in addition to the overall `MaxBytesReader` bound. Concurrent authenticated uploads multiply memory consumption; multipart non-file fields add parser overhead. | Use a smaller in-memory threshold and stream file content with total/per-file limits and global/user resource bounds. | Phase 1B stopgap; streaming in Phase 5 | Concurrent large uploads can exhaust memory or cause severe GC pressure. The total request cap limits one request but not aggregate concurrent memory. |
| F3 | Medium | **Confirmed, with qualification.** [`newHTTPServer`](../main.go#L222) sets `ReadHeaderTimeout` and `IdleTimeout`, but not body-read/write deadlines or an explicit `MaxHeaderBytes` value. Go's `net/http` default still caps headers at 1 MiB. The upload handler reads a request body without a per-request deadline. | Set an explicit header-size cap and use a request-scoped body/read and response deadline suitable for uploads; do not impose a short global write timeout that breaks legitimate large transfers. | Phase 1B; broader limits in Phase 7 | A slow authenticated upload can hold a connection and handler resources much longer than intended; header size currently relies on Go's relatively generous default. |
| F4 | High | **Confirmed.** [`runWithHTTPS`](../main.go#L389) starts TCP at `:<port>`; [`startTCPServer`](../main.go#L298) accepts it on all interfaces. [`acceptConnections`](../main.go#L314) creates an unbounded goroutine/connection per client. The protocol has no authentication or TLS. | Disable raw TCP by default or bind it to loopback/private interfaces; add connection limits and secure any externally enabled listener with a separately designed TLS/authentication policy. | Phase 6 | Internet-reachable clients can use an unauthenticated plaintext service and consume unbounded connection/goroutine resources. Do not expose port 9090 publicly before hardening. |
| F5 | Medium | **Confirmed.** [`newConfiguredHTTPHandler`](../main.go#L213) uses `http.FileServer(http.Dir(...))` without filtering dotfiles, disabling directory listings, or restricting symlinks to the static root. | Wrap static serving to reject dotfiles, directories without an index, and symlinks that escape the static root; test each behavior. | Phase 1B | A mistakenly placed hidden/configuration file or symlink target can be retrieved, and directory contents can be enumerated. |
| F6 | Medium | **Confirmed.** [`run`](../main.go#L383) calls `ListenAndServeTLS` with hardcoded `cert/server.crt` and `cert/server.key` paths relative to the process working directory. | Add validated certificate/key configuration with current paths as defaults for this phase; provision production key material securely. | Phase 1B; proxy TLS termination in Phase 8 | Starting from a different working directory or a deployment without those exact paths fails; certificate provisioning is not deployment-configurable. The local private key is ignored by Git and was not read or exposed during this audit. |
| F7 | Medium | **Confirmed.** [`createUniqueUploadFile`](../main.go#L84) requests mode `0666`, loops without a retry bound, and [`sanitizeUploadFilename`](../main.go#L105) does not reject Windows reserved device names. | Use restrictive file permissions, cap collision retries with an explicit error, and reject reserved names/control characters; Phase 5 should use server-generated storage keys. | Phase 1B stopgap; server-generated keys in Phase 5 | Files may be more broadly readable than intended under permissive permissions; repeated collisions can consume unbounded work, and reserved names can cause platform-specific failures. |
| F8 | Low | **Confirmed.** [`loggingMiddleware`](../main.go#L66) records method, route, and duration but does not capture response status. | Add status capture without logging request paths, headers, credentials, or bodies. | Phase 1B / Phase 7 | Operators cannot distinguish successful and failed requests from access logs, slowing incident diagnosis and operations. |
| F9 | Low | **Confirmed.** [`README.md`](../README.md#L63) and [`README.md`](../README.md#L205) contain the personal absolute path `C:\Users\Pruthvi\GoServe`; the repository root contains `test.txt`. | Use a relative/generic working-directory command and move the example input to `testdata/`, or document why it remains at the root. | Phase 1B | The absolute path reduces portability and exposes a local username; the test fixture at root makes repository layout less clear. No secret was identified in the file. |
| F10 | Medium | **Confirmed.** The repository inventory has no CI workflow, Dockerfile/Compose configuration, or LICENSE. | Add the minimal Go CI workflow in Phase 1; add deployment packaging in Phase 8; resolve the license choice before Phase 9. | Phase 1, Phase 8, Phase 9 respectively | Changes have no automated PR quality gate, deployment is not reproducible from containers, and reuse/distribution terms remain unspecified. |
| F11 | High | **Additional finding.** [`serverConfig.validate`](../config.go#L87) checks that the static directory exists and that the upload path has a valid existing parent, but does not check whether upload storage equals or is nested under static content. [`newConfiguredHTTPHandler`](../main.go#L213) serves that static tree publicly. | Reject canonical path overlap between upload storage and public static content (including symlink-aware resolution); make private storage an invariant in the Phase 5 storage layer. Add a negative test for equal and nested paths. | Add to Phase 1B; enforce again in Phase 5 | With a valid upload credential, a file written under the public static tree can then be fetched without authentication by its filename. |

### Additional test gaps and coupling

- The root [`main.go`](../main.go) combines routing, authentication, logging,
  upload parsing/storage, TCP protocol/listener management, TLS startup, and
  process lifecycle. [`config.go`](../config.go) is comparatively focused;
  the standalone TCP client is isolated. The monolith increases the chance that
  transport, storage, and lifecycle changes affect unrelated behavior.
- Tests cover config defaults/parsing/validation/precedence; static and upload
  responses; upload authentication, body limits, filename sanitation,
  duplicate-name concurrency and partial-write cleanup; TCP boundaries,
  disconnects and timeouts; and HTTP/TCP shutdown. They do **not** test
  constant-time comparison/rate limiting, memory behavior under concurrent
  uploads, slow HTTP bodies, header-size limits, static dotfiles/listings,
  upload/static directory overlap, static symlink escapes, TLS startup against real certificates,
  file permissions, retry exhaustion, reserved names, or access-log status.
- TCP tests exercise loopback listeners and protocol behavior, not externally
  reachable exposure or connection exhaustion. Shutdown tests show active TCP
  connections are closed and handlers are awaited; they do not establish a
  graceful TCP exchange drain.
- Existing project-level roadmap gaps remain: no persistent user/file
  metadata, ownership, quotas, file listing/download/delete API, browser
  gateway, health/readiness endpoints, metrics, CI, or deployment/runbook.
  These are planned capabilities rather than regressions in the current
  learning project.
- Upload storage/write errors are logged with wrapped OS errors. Depending on
  the failure, those errors may include a configured path or sanitized target
  filename. They are not returned to the client, but log access should remain
  restricted and Phase 7 should review path redaction alongside secret
  redaction.

## Phase ordering recommendation

No phase needs to be reordered or split based on this audit. Phase 1's
behavior-preserving refactor followed by Phase 1B hardening is a reasonable
sequence. Add F11's static/upload path separation check to Phase 1B so the
existing upload endpoint cannot accidentally publish files through a
configuration mistake, then retain Phase 5's stronger private-storage
invariant. Keep Phase 6 TCP hardening before any public deployment in Phase 8.
Phase 9 release gates should remain blocked until the raw TCP exposure and
other critical security/operations gates are resolved.

## Baseline checks

Run on Windows (`windows/amd64`), branch `phase-0-audit`, with Go 1.27.0:

```text
> go version
go version go1.27.0 windows/amd64

> go test ./...
ok  	goserve	(cached)
ok  	goserve/cmd/tcp-client	(cached)

> go vet ./...
(no output; exit code 0)

> go test -race ./...
ok  	goserve	(cached)
ok  	goserve/cmd/tcp-client	(cached)
```

All requested checks ran and passed; none were marked NOT RUN. The race tests
also ran successfully on this Windows environment. These results are a
baseline for the current worktree, not proof of production readiness.

## Worktree notes

Before the audit, `git status --short` showed a modified `.gitignore` and two
untracked items: `public/ack.txt` and a filename beginning
`tart language server - activation ...`. None was changed. The certificate/key
and upload directories contain local runtime files and are not tracked. No
contents of the TLS private key were read. Only `docs/AUDIT.md` and
`docs/PROGRESS.md` were created for this Phase 0 task.

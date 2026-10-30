# GoServe

GoServe is a small Go learning project that serves static files over HTTPS,
accepts authenticated multipart uploads, and provides a separate plain-TCP
line-oriented request/response listener. It is not presented as production
ready.

## Features and architecture

- The root Go package starts the HTTPS and TCP listeners.
- HTTPS serves files from the configured static directory and exposes
  `POST /upload`.
- Only uploads require HTTP Basic Authentication. Credentials are read from
  environment variables, not YAML or source code.
- The upload handler enforces the configured total request-body limit,
  sanitizes client filenames, chooses duplicate names with exclusive file
  creation, and removes incomplete files when writes fail.
- HTTP logging records the method, fixed handler route (`/` or `/upload`), and
  request duration. It does not record request paths, query strings, headers,
  credentials, or request bodies.
- TCP listens separately from HTTPS. It handles one newline-terminated
  message of at most 1024 bytes per connection and replies with
  `Received: <message>`. TCP is not protected by HTTPS/TLS.
- HTTPS uses an explicit `http.Server` with a 5-second header-read timeout,
  a 60-second idle timeout, and bounded graceful shutdown. Ctrl+C and
  termination signals initiate shutdown; active TCP clients are closed.

## Prerequisites

- Go 1.27 or newer, as specified by `go.mod`.
- OpenSSL for generating a local development certificate.
- PowerShell commands below are written for Windows. `curl.exe` is used for
  HTTPS checks. The optional `ncat` utility can be used to connect to TCP.

Go downloads the YAML dependency declared in `go.mod` when needed.

## Repository layout

```text
GoServe/
├── cmd/
│   └── tcp-client/       # Standalone TCP command-line client and tests
├── public/               # Static content served over HTTPS
├── cert/                 # Local TLS certificate and private key (generated)
├── uploads/              # Uploaded files (created when needed)
├── config.go             # YAML, defaults, CLI precedence, validation
├── config.example.yaml   # Safe, secret-free example settings
├── config_test.go
├── main.go               # HTTPS/TCP servers, routes, upload and shutdown
├── main_test.go
├── go.mod
└── go.sum
```

`cert/server.key` and uploaded files are local runtime data and should not be
committed. Check `.gitignore` and `git status` before committing.

## Setup

Run commands from the repository root:

```powershell
Set-Location C:\Users\Pruthvi\GoServe
go version
go mod download
```

### Local self-signed HTTPS certificate

The app loads `cert/server.crt` and `cert/server.key`. Generate them only when
both files are absent, so this example does not overwrite existing material:

```powershell
New-Item -ItemType Directory -Force cert | Out-Null
if (!(Test-Path .\cert\server.crt) -and !(Test-Path .\cert\server.key)) {
    openssl req -x509 -newkey rsa:2048 -sha256 -nodes `
        -keyout cert/server.key `
        -out cert/server.crt `
        -days 365 `
        -subj "/CN=localhost" `
        -addext "subjectAltName=DNS:localhost,IP:127.0.0.1"
} else {
    Write-Output "Certificate or key already exists; not overwriting it."
}
```

This creates a self-signed certificate for local development. HTTPS still
encrypts the connection, but clients do not automatically trust this
certificate. For local `curl.exe` checks, `-k` skips certificate validation;
do not use `-k` as a production trust configuration. Production deployments
need a certificate issued by a certificate authority trusted by their clients,
and must securely provision the matching private key.

## Configuration

The defaults are:

| Setting | Default |
| --- | --- |
| HTTPS port | `8443` |
| TCP port | `9090` |
| Static directory | `./public` |
| Upload directory | `./uploads` |
| Maximum upload request size | `10485760` bytes (10 MiB) |

Example YAML in `config.example.yaml`:

```yaml
https_port: 8443
tcp_port: 9090
static_directory: ./public
upload_directory: ./uploads
max_upload_size: 10485760
```

Precedence is **built-in defaults < YAML values < explicitly supplied CLI
flags**. Start with the sample:

```powershell
go run . -config config.example.yaml
```

Override one value at the command line:

```powershell
go run . -config config.example.yaml -https-port 9443
```

Available flags are `-config`, `-https-port`, `-tcp-port`, `-static-dir`,
`-upload-dir`, and `-max-upload-size` (bytes). Legacy aliases `-port` and
`-dir` remain available for HTTPS port and static directory respectively.
Supplying both an alias and its newer flag is an error.

Ports must be in `1..65535`; the static directory must already exist and be a
directory; upload directory paths must have an existing directory parent; and
the upload limit must be positive. Relative paths are resolved from the
process working directory. Unknown YAML fields and multiple YAML documents
are rejected.

Do not add upload credentials, tokens, or private keys to YAML. The sample
configuration contains no secrets.

## Upload authentication and endpoint

The `/upload` endpoint requires both `GOSERVE_UPLOAD_USERNAME` and
`GOSERVE_UPLOAD_PASSWORD`. Startup fails safely if either variable is missing
or blank. Credentials are not configured in YAML and are not written to logs.
Basic Authentication is only protected in transit when used over HTTPS.

In the server PowerShell terminal, enter local development credentials:

```powershell
$env:GOSERVE_UPLOAD_USERNAME = Read-Host "Local upload username"
$securePassword = Read-Host "Local upload password" -AsSecureString
$env:GOSERVE_UPLOAD_PASSWORD = [Net.NetworkCredential]::new("", $securePassword).Password
```

The password is held in the environment for the Go process; do not put a real
password directly in a command, source file, or checked-in file.

The upload endpoint accepts a multipart `file` field. The repository includes
`test.txt` for a local example. In the server terminal, with the variables set:

```powershell
go run . -config config.example.yaml
```

In a second terminal, set the same local credentials and upload `test.txt`;
curl prompts for the password:

```powershell
$env:GOSERVE_UPLOAD_USERNAME = Read-Host "Local upload username"
$securePassword = Read-Host "Local upload password" -AsSecureString
$env:GOSERVE_UPLOAD_PASSWORD = [Net.NetworkCredential]::new("", $securePassword).Password
curl.exe -k -i -u $env:GOSERVE_UPLOAD_USERNAME `
    -F "file=@.\test.txt" https://localhost:8443/upload
```

Expected successful response includes `File uploaded successfully`. Repeating
the request saves a uniquely suffixed filename rather than overwriting the
existing upload.

Status behavior includes:

- `401 Unauthorized` for missing or incorrect credentials.
- `503 Service Unavailable` if the upload handler is used without configured
  authentication credentials.
- `405 Method Not Allowed` for a non-POST upload request with valid
  credentials.
- `400 Bad Request` for malformed multipart data, missing `file`, or invalid
  filenames.
- `413 Request Entity Too Large` when the configured total request-body limit
  is exceeded.
- `500 Internal Server Error` for upload storage/write failures.

The configured size limit applies to the whole multipart request body, so
multipart framing and fields count toward it, not just file bytes.

## Run and test the servers

With the certificate and both authentication variables configured, start the
app in Terminal 1:

```powershell
Set-Location C:\Users\Pruthvi\GoServe
go run . -config config.example.yaml
```

In Terminal 2, check static HTTPS content:

```powershell
curl.exe -k -i https://localhost:8443/
curl.exe -k -i https://localhost:8443/hello.txt
```

Check the protected upload route (this request omits the file field and should
return `400 Bad Request` after successful authentication):

```powershell
curl.exe -k -i -u $env:GOSERVE_UPLOAD_USERNAME https://localhost:8443/upload
```

Check TCP using the included standalone client:

```powershell
go run ./cmd/tcp-client "hello GoServe"
```

Expected response:

```text
Received: hello GoServe
```

The client uses `localhost:9090` by default; override it with
`-addr host:port`. It accepts one command-line message, rejects multiline or
over-1024-byte messages, and has a 5-second connection/read/write deadline.
Alternatively, if Ncat is installed:

```powershell
ncat localhost 9090
```

Type a line and press Enter. The listener closes the connection after its
single response. Ctrl+C in Terminal 1 requests graceful shutdown; active HTTPS
requests are allowed to finish within the 10-second shutdown bound, and the
TCP listener and active clients are closed.

## Tests and static checks

```powershell
gofmt -w config.go config_test.go main.go main_test.go cmd\tcp-client\main.go cmd\tcp-client\main_test.go
go test ./...
go vet ./...
go test -race ./...
```

Tests cover configuration defaults/parsing/validation/precedence; static and
upload routes, authentication, body limits, and upload filename/partial-file
handling; TCP line framing, limits, timeout and disconnect behavior; the TCP
client; and server shutdown paths. Tests provide evidence for covered cases,
not proof that all possible race conditions or deployment environments are
handled.

## Known limitations

- This is a learning project and is not claimed to be production ready.
- Local self-signed TLS is not trusted by clients by default. Production
  deployments must provision trusted certificates and private keys securely.
- The separate TCP protocol is plain, unencrypted TCP.
- Static files are public; Basic Authentication protects uploads only.
- Uploads are stored on local disk. There is no database, upload quota,
  account system, or management interface.
- TCP request handling accepts one line per connection and has no authentication
  or TLS.
- Automated tests do not replace integration tests in the target deployment
  environment.

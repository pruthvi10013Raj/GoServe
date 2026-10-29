# GoServe

GoServe serves static files over HTTPS, accepts authenticated file uploads,
and runs a plain TCP request-response listener.

## Configuration

Without a config file, GoServe uses these defaults:

| Setting | Default |
| --- | --- |
| HTTPS port | `8443` |
| TCP port | `9090` |
| Static directory | `./public` |
| Upload directory | `./uploads` |
| Maximum upload size | `10485760` bytes (10 MiB) |

Start with the included example:

```powershell
go run . -config config.example.yaml
```

The YAML keys and values are:

```yaml
https_port: 8443
tcp_port: 9090
static_directory: ./public
upload_directory: ./uploads
max_upload_size: 10485760
```

The precedence is built-in defaults, then values from YAML, then explicitly
provided command-line flags. For example, this uses the example values except
for HTTPS port:

```powershell
go run . -config config.example.yaml -https-port 9443
```

Available server flags are `-config`, `-https-port`, `-tcp-port`, `-static-dir`,
`-upload-dir`, and `-max-upload-size` (bytes). The older `-port` and `-dir`
flags remain as aliases for `-https-port` and `-static-dir`.

Ports must be in the range 1–65535, directories must not be blank, the static
directory must already exist, and the upload limit must be positive. The upload
directory may be created at upload time if its parent directory exists. Relative
paths are interpreted from the process working directory.

Do not put passwords, tokens, or private keys in the YAML file. The HTTPS
certificate and private key are loaded separately from `cert/server.crt` and
`cert/server.key`.

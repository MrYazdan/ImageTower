# Tower

Lightweight, production-ready Docker image watcher and updater written in Go. Monitors container image registries (
GitLab, Docker Hub, etc.) for digest changes, pulls updates using native Docker CLI and credentials, and executes
deployment commands automatically.

## Features

- **Digest-based updates** - resolves image digests via registry HEAD requests without pulling
- **Lightweight & Fast** - direct CLI pull with zero heavy SDK bloat or complex dependencies
- **GitLab & Docker Hub support** - standard JWT token flow and Www-Authenticate header parsing
- **Built-in Log Rotation** - automatic log file rotation with configurable max size (e.g. 10MB)
- **Atomic state management** - JSON state file with atomic rename to prevent re-deploying on restarts
- **Concurrent scheduling** - worker pool with per-image locking to prevent duplicate runs
- **Digest verification** - after pull, the locally stored image digest is inspected and matched against the registry
  digest before deploying (defense in depth)
- **Command execution** - timeout-enforced deployment commands with full error logging; the whole process group is
  killed on timeout/cancel so no orphaned children linger
- **Graceful shutdown** - SIGTERM/SIGINT stops new cycles but lets in-flight pulls/deploys finish within
  `shutdown_timeout` before force-cancelling
- **Clean logging** - human-readable text logs (or structured JSON) with concise summaries
- **Docker config auth** - reads `~/.docker/config.json` for registry credentials automatically

## Quick Start

```bash
go build -o tower ./cmd/tower
./tower -config config.example.yaml
```

## Configuration

```yaml
log_level: info
log_format: text
log_file: ./tower.log
log_max_size_mb: 10
log_max_backups: 3

check_interval: 30s
concurrency: 4
command_timeout: 2m
shutdown_timeout: 5m
state_file: ./state.json
docker_config_path: ~/.docker/config.json
insecure_skip_verify: false

images:
  - name: gitlab.example.com:5050/mygroup/myproject/myapp
    tag: latest
    command: "docker compose up -d myapp"
```

| Field                  | Default                 | Description                                                     |
|------------------------|-------------------------|-----------------------------------------------------------------|
| `log_level`            | `info`                  | `debug`, `info`, `warn`, `error`                                |
| `log_format`           | `text`                  | `text` (clean & concise) or `json`                              |
| `log_file`             | `""`                    | Path to log file (optional; logs to stdout if empty)            |
| `log_max_size_mb`      | `10`                    | Max log file size in MB before rotation                         |
| `log_max_backups`      | `3`                     | Number of rotated backups to keep                               |
| `check_interval`       | `30s`                   | Minimum 5s                                                      |
| `concurrency`          | `4`                     | Max parallel image checks (1-128)                               |
| `command_timeout`      | `2m`                    | Timeout for update commands                                     |
| `shutdown_timeout`     | `5m`                    | Grace period for in-flight work on shutdown before force-cancel |
| `state_file`           | `./state.json`          | Persistent state path                                           |
| `docker_config_path`   | `~/.docker/config.json` | Docker config for registry credentials                          |
| `insecure_skip_verify` | `false`                 | Skip SSL verification for internal registries                   |

## Authentication

Tower uses registry credentials from:

1. **`docker_config_path`** - reads auth credentials from `~/.docker/config.json` (created automatically by
   `docker login`)
2. **`auth_env`** (optional) - environment variable containing token or `user:password`

After `docker login gitlab.example.com:5050`, credentials are automatically detected.

## Development

```bash
go test ./...
go build ./...
```

## CI/CD & Automated Releases

Tower includes a production-ready GitHub Actions workflow (`.github/workflows/ci-cd.yaml`):

### 1. Push to `main` (Continuous Integration)
- Runs unit tests and race detection (`go test -v -race ./...`).
- Validates multi-architecture container builds (`linux/amd64` & `linux/arm64`) with Docker Buildx (**without pushing** to any container registry).

### 2. Git Tag Push (Automated Binary Releases & Changelog)
When a semantic version tag is pushed (e.g. `v1.0.0`):
1. **Tests & Build Validation**: Validates all tests and ensures container builds succeed.
2. **Binary Compilation**: Builds standalone, statically linked binaries for `linux/amd64` and `linux/arm64`.
3. **Automated Changelog**: Parses Gitmoji and conventional commits with `git-cliff` to generate categorized release notes.
4. **GitHub Release**: Publishes a new GitHub Release with the changelog, executable binaries, and example configuration.

To release a new version:
```bash
git tag v1.0.0
git push origin v1.0.0
```


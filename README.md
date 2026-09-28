# Tower

[![CI Status](https://img.shields.io/github/actions/workflow/status/MrYazdan/Tower/ci-cd.yaml?branch=main&style=flat-square&logo=githubactions&logoColor=white)](https://github.com/MrYazdan/Tower/actions)
[![Latest Release](https://img.shields.io/github/v/release/MrYazdan/Tower?style=flat-square&color=3b82f6&logo=github)](https://github.com/MrYazdan/Tower/releases)
[![Go Version](https://img.shields.io/github/go-mod/go-version/MrYazdan/Tower?style=flat-square&logo=go)](https://golang.org)
[![Coverage](.github/badges/coverage.svg)](https://github.com/MrYazdan/Tower/actions)
[![Go Report Card](https://goreportcard.com/badge/github.com/MrYazdan/Tower)](https://goreportcard.com/report/github.com/MrYazdan/Tower)

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

## Production Deployment (Systemd Service)

You can run Tower as a robust, background systemd daemon on Linux with automatic restarts on crash or system reboot.

### Step 1: Download & Install Binary

Download the latest static binary for your architecture from GitHub Releases and place it in `/usr/local/bin`:

```bash
# Detect architecture (amd64 or arm64)
ARCH=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')

# Download latest release binary
sudo curl -sSL -o /usr/local/bin/tower \
  "https://github.com/MrYazdan/Tower/releases/latest/download/tower-linux-${ARCH}"

# Make it executable
sudo chmod +x /usr/local/bin/tower
```

Verify installation:
```bash
tower -help
```

### Step 2: Configure Tower

Create directories for configuration, persistent state, and logs:

```bash
sudo mkdir -p /etc/tower /var/lib/tower /var/log/tower
sudo cp config.example.yaml /etc/tower/config.yaml
sudo chmod 600 /etc/tower/config.yaml
```

Update `/etc/tower/config.yaml` with your target images and deployment commands:
```yaml
log_level: info
log_format: text
log_file: /var/log/tower/tower.log
log_max_size_mb: 10
log_max_backups: 3

check_interval: 30s
concurrency: 4
command_timeout: 2m
shutdown_timeout: 5m
state_file: /var/lib/tower/state.json

images:
  - name: myregistry.com/myteam/webapp
    tag: latest
    command: "docker compose -f /opt/webapp/docker-compose.yaml up -d"
```

> **Note:** Tower automatically defaults to `/etc/tower/config.yaml` if no `-config` flag or local `./config.yaml` is provided.

### Step 3: Create Systemd Service File

Create `/etc/systemd/system/tower.service`:

```bash
sudo tee /etc/systemd/system/tower.service > /dev/null << 'EOF'
[Unit]
Description=Tower - Docker Image Watcher & Deployer
Documentation=https://github.com/MrYazdan/Tower
After=network-online.target docker.service
Wants=network-online.target docker.service

[Service]
Type=simple
ExecStart=/usr/local/bin/tower -config /etc/tower/config.yaml
Restart=always
RestartSec=5s
KillMode=mixed
TimeoutStopSec=300s

# Optional: run as a dedicated user (user must belong to the 'docker' group)
# User=tower
# Group=docker

[Install]
WantedBy=multi-user.target
EOF
```

### Step 4: Enable & Start Service

Reload systemd daemon, enable auto-start on boot, and start Tower:

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now tower
```

Check service status:
```bash
sudo systemctl status tower
```

### Step 5: Monitoring Logs

Tower supports both real-time systemd journal inspection and persistent rotating log files simultaneously:

1. **Live Systemd Journal:**
   ```bash
   # Follow live logs from standard output
   sudo journalctl -u tower -f

   # Follow clean raw logs without systemd timestamps/prefix
   sudo journalctl -u tower -f -o cat
   ```

2. **Persistent Log File (Built-in Rotation):**
   When `log_file` is specified in your config (e.g. `/var/log/tower/tower.log`), Tower's internal rotator mirrors logs to both stdout and the file with automatic size-based rotation:
   ```bash
   sudo tail -f /var/log/tower/tower.log
   ```

## Development

```bash
go test ./...
go build ./...
```

## CI/CD & Automated Releases

Tower includes a production-ready GitHub Actions workflow (`.github/workflows/ci-cd.yaml`):

### 1. Push to `main` (Continuous Integration)
- Automatically runs unit tests and race detection (`go test -v -race ./...`).
- Verifies module dependencies and code formatting.

### 2. Git Tag Push (Automated Binary Releases & Changelog)
When a semantic version tag is pushed (e.g. `v1.0.0`):
1. **Tests**: Validates all tests before release.
2. **Binary Compilation**: Builds standalone, statically linked binaries for `linux/amd64` and `linux/arm64`.
3. **Automated Changelog**: Parses Gitmoji and conventional commits with `git-cliff` to generate categorized release notes.
4. **GitHub Release**: Publishes a new GitHub Release with the changelog, executable binaries, and example configuration.

To release a new version:
```bash
git tag v1.0.0
git push origin v1.0.0
```


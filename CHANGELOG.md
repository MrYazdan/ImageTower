# Changelog

All notable changes to this project are documented in this file.

## Recent Changes

- :memo: docs(readme): add comprehensive documentation, architecture overview, and quickstart guide (b11adad)
- :recycle: refactor(config): add directory writability check and expand home directory path (44c96d6)
- :sparkles: feat(docker): add multi-stage Dockerfile for unprivileged container deployment (255fcce)
- :sparkles: feat(cmd): build main entry point with slog and signal handling (cb14e1b)
- :sparkles: feat(rotator): add thread-safe log file rotation without external dependencies (6453243)
- :test_tube: test(scheduler): add unit tests for scheduler workflows and edge cases (0f407e7)
- :sparkles: feat(scheduler): implement graceful shutdown with configurable drain period (c9b87a9)
- :rotating_light: fix(scheduler): prevent concurrent duplicate runs with per-image locking (4677c86)
- :sparkles: feat(scheduler): implement worker pool and image checking orchestration (e313998)
- :sparkles: feat(docker): verify pulled image RepoDigests against registry digest (eba029a)
- :sparkles: feat(docker): add lightweight Docker CLI puller (d417947)
- :rotating_light: fix(runner): prevent zombie processes by killing process groups (279aacc)
- :sparkles: feat(runner): implement command execution with independent timeout (ee6fb81)
- :test_tube: test(registry): add mock server tests for resolver and auth flows (c7d6ed6)
- :recycle: refactor(registry): add fallback body hashing and basic auth support (2e0369b)
- :sparkles: feat(registry): support Bearer token challenge-response authentication (46f152a)
- :sparkles: feat(registry): implement OCI manifest digest resolver via HTTP HEAD (de36ee4)
- :test_tube: test(dockerconfig): add comprehensive test suite for credential extraction (9220100)
- :sparkles: feat(dockerconfig): support Docker Hub host aliases and credential candidates (840c9d7)
- :sparkles: feat(dockerconfig): parse docker auth credentials from config.json (f051efc)
- :recycle: refactor(state): ensure atomic file writes with fsync and temp rename (d721fee)
- :sparkles: feat(state): implement thread-safe persistent state management (24e8c5b)
- :test_tube: test(config): add unit tests for config validation and defaults (fb73655)
- :sparkles: feat(config): define base configuration structures and yaml loader (c6d741c)
- :gear: chore(init): initialize Go module and repository structure (0929ded)

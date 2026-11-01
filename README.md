# CronSoul

**Local-first. Self-hosted. Your schedules, your data, your infrastructure.**

CronSoul is an HTTP endpoint scheduler and operations console. It stores schedules, run history, accounts, and notification configuration in a local SQLite database managed by the operator. Jobs execute from the CronSoul host to destinations explicitly permitted by its egress allowlist. No hosted CronSoul service or account is required.

Created and maintained by **[@user4xn](https://github.com/user4xn)**.

> **Production note:** CronSoul is an operational tool, not a managed service. You own TLS, network exposure, backups, upgrades, credentials, and recovery. Read [Container deployment](docs/CONTAINER.md) before exposing an instance.

## Features

- One Go binary with the vanilla JavaScript SPA embedded; no Node runtime or frontend build step.
- Cron scheduling with timezone-aware previews, manual runs, run history, and timeline.
- Retry policies, singleton execution, idempotency support, assertions, simulated time, and dry-run query markers.
- RBAC (`viewer`, `qa`, `admin`, `superadmin`), mandatory initial password change, session auth, governance, and audit log.
- Egress allowlist with private/metadata-address safeguards, redirect refusal, timeout and response-size limits.
- SMTP and Telegram notification channels with event subscriptions and an outbox.
- SQLite persistence; no external database or application service required.
- Settings screen that reports instance mode and runtime limits read-only, explains the environment variables behind them, and links back to this repository.

## Quick start

### Go

Requirements: Go 1.27 or newer.

```bash
git clone https://github.com/user4xn/cronsoul.git
cd cronsoul
go run ./cmd/cronsoul
```

Open <http://127.0.0.1:9470>. The first boot creates `admin` with temporary password `12345678` and forces a password change. Change it immediately. For a persistent local database:

```bash
CRONSOUL_DB=./cronsoul.db go run ./cmd/cronsoul
```

### Docker image

Published image: [`ghcr.io/user4xn/cronsoul`](https://github.com/user4xn/cronsoul/pkgs/container/cronsoul). Compose uses the `master` tag. The `latest` tag is reserved for approved version-tag releases.

```bash
docker pull ghcr.io/user4xn/cronsoul:master
docker compose up -d
```

Compose binds to `127.0.0.1:9470` and stores the database and generated encryption key in the `cronsoul-data` volume. This default is for local evaluation only (`CRONSOUL_ENV=dev`, TLS off); do not expose it directly to the internet.

See [Container deployment](docs/CONTAINER.md) for production environment variables, reverse-proxy setup, upgrades, backups, and image tags. See [How to use CronSoul](docs/HOW-TO-USE.md) for the full operator guide.

## Local development and checks

`make check` runs everything CI enforces (gofmt, vet, tests, race, build, UI syntax, whitespace):

```bash
make check
```

The individual steps:

```bash
gofmt -l .
go vet ./...
go test -count=1 ./...
go test -race -count=1 ./...
go build ./cmd/cronsoul
node --check cmd/cronsoul/ui/app.js
git diff --check
```

Docker build:

```bash
docker build -t cronsoul:local .
docker run --rm -p 127.0.0.1:9470:9470 -v cronsoul-data:/data \
  -e CRONSOUL_ENV=dev -e CRONSOUL_DB=/data/cronsoul.db \
  -e CRONSOUL_SECRET_KEY_FILE=/data/secret.key cronsoul:local
```

## Public repository scope

The public tree is the application source, tests, Docker/Compose files, CI and publish workflows, and the operator documentation under `docs/`. Credentials, databases, generated keys, and deployment-specific configuration are not tracked; `.env.example` contains placeholders only.

Earlier drafts — mockups, PRDs, brand notes, and internal research — were removed from the tree during the public-release cleanup. **Deleting files does not erase them from earlier Git history.** Review the full history before publishing or mirroring this repository, and prefer a squashed public history if those drafts must not be readable.

The repository does not include a `LICENSE` file yet. Public visibility and a publicly pullable container image do not grant permission to reuse, modify, or redistribute the source. Add a license before describing CronSoul as open source.

## Documentation

- [How to use CronSoul](docs/HOW-TO-USE.md) — end-to-end operator guide for every screen.
- [Container deployment](docs/CONTAINER.md) — GHCR image, environment variables, TLS, storage, backup, and upgrades.
- [HTTP API reference](docs/openapi.yaml) — OpenAPI 3.0 description of every route the console calls.
- [Public repository surface](docs/PUBLIC-SURFACE.md) — tracked-file inventory and pre-publication checklist.
- [Database schema](docs/cronsoul-schema.sql) — reference only; the embedded migrations in `internal/store/migrations/` are authoritative.

The API under `/api/v1` is consumed by the embedded UI. It is documented for operators and integrators but is not yet a versioned contract; routes may change between releases.

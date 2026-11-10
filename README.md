# 🕒 CronSoul

> Self-hosted HTTP scheduler and operations console — cron for endpoints, not for servers.

CronSoul calls the URLs you register, on the schedule you give it, and shows exactly what happened. Schedules, run history, users and notification settings live in one SQLite file you own.

No hosted account, no vendor lock-in, no external database. It ships as a single Go binary with the console already embedded.

[![ci](https://github.com/user4xn/cronsoul-public/actions/workflows/ci.yml/badge.svg)](https://github.com/user4xn/cronsoul-public/actions/workflows/ci.yml)

---

## ✨ Features

- **Cron with real timezones** — five-field expressions, one timezone per endpoint, and a preview of the next fire times instead of guesswork.
- **Run history that explains failures** — status, HTTP code, duration, attempts, response excerpt, and copy-as-cURL to reproduce the request.
- **Timeline** — an hour-by-hour grid per endpoint for today or the last week, so failure clusters and missed windows are visible at a glance.
- **Retries and idempotency** — fixed or exponential backoff, singleton execution, and a run identity the receiver can deduplicate.
- **Assertions** — check status, headers or JSON body; assertion results are reported separately from HTTP success.
- **Egress allowlist** — outbound calls only reach hosts you allow. Private, loopback and cloud-metadata ranges stay blocked, redirects are refused, responses are capped.
- **Notifications** — SMTP and Telegram channels, per-channel event subscriptions, and an outbox with delivery state.
- **RBAC and audit** — `viewer` / `qa` / `admin` / `superadmin`, forced first-login password change, and an audit trail for every governance change.
- **One binary, no Node** — Go + chi + SQLite, with the vanilla-JS console compiled in via `//go:embed`.

---

## 📋 Requirements

**Docker (recommended)**
- Docker with Compose on any Linux host
- amd64 or arm64 — both images are published

**From source**
- Go 1.27 or newer

No database server, no Node.js, and no separate frontend build step.

---

## 🐳 Install with Docker (Recommended)

The image is public and needs no environment variables for a first run. It defaults to `dev`, keeps its database on `/data`, and generates the encryption key once on that volume.

**1. Start the container:**

```bash
docker run -d --name cronsoul \
  -p 127.0.0.1:9470:9470 \
  -v cronsoul-data:/data \
  ghcr.io/user4xn/cronsoul-public:master
```

**2. Open the console:**

```
http://127.0.0.1:9470
```

**3. Sign in:**

```
admin / 12345678
```

The console forces a password change at first login. The `cronsoul-data` volume keeps jobs, runs, users and the encryption key across restarts and upgrades.

**Or with Compose:**

```yaml
services:
  cronsoul:
    image: ghcr.io/user4xn/cronsoul-public:master
    container_name: cronsoul
    restart: unless-stopped
    ports:
      - "127.0.0.1:9470:9470"
    volumes:
      - cronsoul-data:/data

volumes:
  cronsoul-data:
```

**Quick Start : If you want to use in Production. Need Configure This**
```yaml
    ...
    enviroment:
      - CRONSOUL_SECRET_KEY = {some_key}
      - CRONSOUL_BASE_URL = {your.addess.id}
      - CRONSOUL_TLS_MODE = {cert/proxy}
      - CRONSOUL_TLS_CERT = {cert_key} //if you choose cert
    ...

```

**Updating:**

```bash
docker compose pull && docker compose up -d
```

`vX.Y.Z` tags are published for reviewed releases; `master` follows the branch and is meant for evaluation.

> The default is loopback-only, `dev`, TLS off. Do not expose it to another machine as-is — see [Security model](#-security-model) and [docs/CONTAINER.md](docs/CONTAINER.md).

---

## 🚀 Quick Start (Build from Source)

```bash
git clone https://github.com/user4xn/cronsoul-public
cd cronsoul-public
go run ./cmd/cronsoul
```

Open [http://127.0.0.1:9470](http://127.0.0.1:9470). Data stays in memory, which is fine for a look around. To keep it on disk:

```bash
make dev-file        # or: CRONSOUL_DB=./cronsoul.db go run ./cmd/cronsoul
```

Build a static binary:

```bash
make build           # writes ./cronsoul
```

---

## ⚙️ Setup Guide (5 steps)

**Step 1 — Sign in and change the password.** Use the seeded `admin` account once, set a new password, then add named users under **Users** with the lowest role that fits.

**Step 2 — Allow your destinations.** CronSoul refuses outbound calls unless the host (or `host:port`) is on the **Allowlist**. Add the API hosts your jobs call; private and metadata addresses stay blocked even when listed.

**Step 3 — Create an endpoint.** Give it a key, a URL, a cron expression and a timezone. Add headers, query, body or auth as needed, then set timeout, retries and assertions. The schedule preview shows when it will actually fire.

**Step 4 — Run it once.** Press **Play** to queue a manual run. A `409` means a singleton endpoint is still running. Inspect the result in **Runs**: request, response excerpt, attempts, assertions.

**Step 5 — Wire notifications.** Add an SMTP or Telegram channel under **Channels**, subscribe it to events, send a test, then watch **Alerts** and the outbox. A configured channel is not proof of delivery — test it against a real recipient.

---

## 🏗️ Architecture

```
browser ──HTTP──▶ CronSoul (single Go binary)
                    │  chi router: /api/v1 + embedded SPA
                    ├─ SQLite (jobs, runs, users, settings, audit)
                    ├─ scheduler ─▶ runner ─▶ HTTPS target
                    └─ notifier ─▶ SMTP / Telegram
```

The scheduler ticks inside the same process. Each run is written before and after the request, so a crash leaves evidence instead of a silent gap. Outbound calls pass through the allowlist and policy checks in `internal/runner`.

| Path | Purpose |
|---|---|
| `cmd/cronsoul` | entrypoint, wiring, embedded UI assets |
| `internal/jobs` | endpoint CRUD, cron validation, manual play |
| `internal/runner` | HTTP execution, retries, assertions, egress policy |
| `internal/scheduler` | cron loop and missed-window detection |
| `internal/notify` | channels, event subscriptions, outbox delivery |
| `internal/ops` | settings, users, allowlist, audit |
| `internal/auth` | sessions, RBAC, password handling |
| `internal/store` | SQLite access and embedded migrations |

---

## ⚙️ Environment Variables

Every variable is optional for a local run. The image sets the first four, so a plain `docker run` works.

| Variable | Default (image) | Description |
|---|---|---|
| `CRONSOUL_ENV` | `dev` | `production` enables the full startup validation. |
| `CRONSOUL_DB` | `/data/cronsoul.db` | SQLite path; outside dev it must be a file, not `:memory:`. |
| `CRONSOUL_SECRET_KEY_FILE` | `/data/secret.key` | Key file, created once and reused; back it up with the database. |
| `CRONSOUL_TLS_MODE` | `off` | `proxy` behind a TLS terminator, or `cert`; `off` is dev-only. |
| `CRONSOUL_BASE_URL` | _(unset)_ | Required outside dev; must be the public HTTPS URL. |
| `CRONSOUL_SECRET_KEY` | _(unset)_ | Alternative to the key file: 32 bytes, raw or base64. |
| `CRONSOUL_LISTEN` | `:9470` | Listen address inside the container. |
| `CRONSOUL_DISPLAY_TZ` | `Asia/Jakarta` | Default console timezone, changeable in Settings. |
| `CRONSOUL_SESSION_TTL` | `12h` | Session lifetime, Go duration syntax. |
| `CRONSOUL_TLS_CERT` / `_KEY` | _(unset)_ | Certificate and key paths when `TLS_MODE=cert`. |

Full reference, reverse-proxy example, backup and upgrade notes: [docs/CONTAINER.md](docs/CONTAINER.md).

---

## 🔐 Security model

- **Production is explicit.** `CRONSOUL_ENV=production` refuses to start without an HTTPS base URL, a file-backed database, a 32-byte key, and TLS via proxy or certificate.
- **Nothing is reachable by default.** The egress allowlist starts empty; jobs fail until you add a host. Loopback, link-local, multicast and cloud-metadata ranges remain blocked.
- **Redirects are not followed**, timeouts are enforced, and response bodies are truncated (32 KiB default, 1 MiB hard cap).
- **Secrets are encrypted at rest** with the instance key. Rotating that key makes existing channel credentials unreadable — re-enter them.
- **Sessions are HTTP-only cookies** and are invalidated on logout. The seeded `admin` account must change its password before doing anything else.

---

## ⚠️ Known Limitations

- **Single instance.** SQLite plus an in-process scheduler; no clustering or HA.
- **No built-in public TLS.** CronSoul serves plain HTTP unless you use `cert` mode or put a reverse proxy in front.
- **API is not a stable contract.** `/api/v1` is documented in [docs/openapi.yaml](docs/openapi.yaml) for operators, but routes may change between releases.
- **English-only console** for now.

---

## 🧪 Development

```bash
make check     # gofmt, vet, tests, race, build, UI syntax, whitespace
make test      # unit tests only
make dev-file  # run with a local database
```

`make check` is exactly what CI runs. Tests cover egress policy, retry and assertion logic, scheduler behaviour, and the HTTP handlers.

Build the container locally:

```bash
docker build -t cronsoul:local .
docker run --rm -p 127.0.0.1:9470:9470 -v cronsoul-data:/data cronsoul:local
```

---

## 📦 Repository Scope

The public tree is the application source, tests, Docker and Compose files, CI/publish workflows, and the operator documentation under `docs/`. Credentials, databases, generated keys and deployment-specific configuration are not tracked; `.env.example` holds placeholders only.

Earlier drafts — mockups, PRDs, brand notes and internal research — were removed before publication, and the public repository starts from a single clean commit. Drafts may still exist in older private history; they are not part of this repository.

The project is MIT licensed — see [LICENSE](LICENSE).

---

## 📚 Documentation

- [How to use CronSoul](docs/HOW-TO-USE.md) — every screen, end to end.
- [Container deployment](docs/CONTAINER.md) — environment, TLS, storage, backup, upgrades.
- [HTTP API](docs/openapi.yaml) — OpenAPI 3 description of the console's routes.
- [Database schema](docs/cronsoul-schema.sql) — reference; embedded migrations are authoritative.
- [Public surface audit](docs/PUBLIC-SURFACE.md) — what is tracked and what was removed.

---

## 📄 License

MIT — see [LICENSE](LICENSE). Use it, fork it, ship it.

---

Built by [@user4xn](https://github.com/user4xn).

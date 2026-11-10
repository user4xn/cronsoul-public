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

## 🚀 Try it in two minutes

This is the evaluation setup: one container, one volume, no configuration. It is **not** a production deployment — see [Running in production](#-running-in-production).

```bash
docker run -d --name cronsoul \
  -p 127.0.0.1:9470:9470 \
  -v cronsoul-data:/data \
  ghcr.io/user4xn/cronsoul-public:latest
```

Open `http://127.0.0.1:9470` and sign in with `admin` / `12345678`. The console forces a password change before anything else is reachable.

What happened behind that command:

| Piece | Meaning |
|---|---|
| `-p 127.0.0.1:9470:9470` | Only your machine can reach the console. Remove `127.0.0.1` **only** after you put TLS in front (below). |
| `-v cronsoul-data:/data` | Jobs, runs, users, settings and the encryption key. Delete the volume and you delete the instance. |
| no `-e` flags | The image defaults to `dev`, TLS off, SQLite at `/data/cronsoul.db`, key at `/data/secret.key` (generated once). |

**Image tags:** `latest` and `0.1.x` are released builds. `master` follows the branch and can be broken — use it only if you are testing the next release.

**Or with Compose** (`docker compose up -d`):

```yaml
services:
  cronsoul:
    image: ghcr.io/user4xn/cronsoul-public:latest
    container_name: cronsoul
    restart: unless-stopped
    ports:
      - "127.0.0.1:9470:9470"
    volumes:
      - cronsoul-data:/data

volumes:
  cronsoul-data:
```

---

## 🧭 First five minutes inside the console

1. **Change the password**, then add named users under **Users** with the lowest role that fits (`viewer` for read-only, `qa` to trigger runs, `admin` to edit endpoints).
2. **Allowlist your destination.** Outbound calls are blocked by default. Add the API host — `api.example.com` or `api.example.com:8443` — under **Allowlist** before creating the endpoint.
3. **Create an endpoint:** key, URL, cron expression, timezone. The schedule preview shows the next real fire times in that timezone.
4. **Press Play** to queue a manual run. `409` means a singleton endpoint is still running.
5. **Read the run** in **Runs**: request sent, response excerpt, attempts, assertion results.

> If step 4 fails with *not allowlisted*, you skipped step 2. That is the most common first-run mistake, and it is deliberate: CronSoul will not call a host you have not approved.

---

## 🔧 Running in production

Production is a different mode, not a different image. `CRONSOUL_ENV=production` refuses to start unless the instance is configured safely, so a misconfigured deploy fails loudly at boot instead of serving an insecure console.

Pick **one** of the two TLS paths.

### Option A — TLS terminated by a reverse proxy (recommended)

CronSoul speaks plain HTTP on a private port; Caddy, nginx, Traefik or your platform ingress handles certificates and HTTPS.

```yaml
services:
  cronsoul:
    image: ghcr.io/user4xn/cronsoul-public:latest
    restart: unless-stopped
    environment:
      CRONSOUL_ENV: production
      CRONSOUL_BASE_URL: https://cron.example.com
      CRONSOUL_TLS_MODE: proxy
      CRONSOUL_DB: /data/cronsoul.db
      CRONSOUL_SECRET_KEY_FILE: /data/secret.key
    volumes:
      - cronsoul-data:/data
    ports:
      - "127.0.0.1:9470:9470"   # proxy reaches it; the internet does not

volumes:
  cronsoul-data:
```

Minimal Caddy in front of it:

```caddyfile
cron.example.com {
    reverse_proxy 127.0.0.1:9470
}
```

Caddy obtains and renews the certificate itself. With nginx, point `proxy_pass http://127.0.0.1:9470;` at the same address and run certbot.

### Option B — TLS served by CronSoul itself

Only pick this when you cannot run a proxy. CronSoul then loads the certificate and key directly, and **you** own renewal.

```yaml
services:
  cronsoul:
    image: ghcr.io/user4xn/cronsoul-public:latest
    restart: unless-stopped
    environment:
      CRONSOUL_ENV: production
      CRONSOUL_BASE_URL: https://cron.example.com
      CRONSOUL_TLS_MODE: cert
      CRONSOUL_TLS_CERT: /etc/cronsoul/fullchain.pem
      CRONSOUL_TLS_KEY: /etc/cronsoul/privkey.pem
      CRONSOUL_DB: /data/cronsoul.db
      CRONSOUL_SECRET_KEY_FILE: /data/secret.key
    volumes:
      - cronsoul-data:/data
      - /etc/letsencrypt/live/cron.example.com/fullchain.pem:/etc/cronsoul/fullchain.pem:ro
      - /etc/letsencrypt/live/cron.example.com/privkey.pem:/etc/cronsoul/privkey.pem:ro
    ports:
      - "443:9470"

volumes:
  cronsoul-data:
```

Which certificate files:

| Variable | File | Notes |
|---|---|---|
| `CRONSOUL_TLS_CERT` | `fullchain.pem` | Leaf **plus** intermediate, leaf first. Not the bare leaf, not `chain.pem`, not a CA bundle. |
| `CRONSOUL_TLS_KEY` | `privkey.pem` | Unencrypted PEM. CronSoul has no passphrase option. |

Let's Encrypt keys are `600 root`. Run the container as root or match the file ownership, otherwise startup fails with `permission denied`.

Verify after boot — this must print `2` (leaf + intermediate):

```bash
openssl s_client -connect cron.example.com:443 -servername cron.example.com -showcerts </dev/null \
  | grep -c 'BEGIN CERTIFICATE'
```

### What production validation actually checks

CronSoul exits at startup if any of these is wrong:

| Check | Failure message |
|---|---|
| `CRONSOUL_BASE_URL` set and `https://` | `CRONSOUL_BASE_URL is required` / `must be a valid https URL outside dev` |
| `CRONSOUL_TLS_MODE` is `proxy` or `cert` | `CRONSOUL_TLS_MODE=off is allowed only in dev` |
| `cert` mode has both cert and key paths | `TLS cert mode requires CRONSOUL_TLS_CERT and CRONSOUL_TLS_KEY` |
| `CRONSOUL_DB` is a file, not `:memory:` | `CRONSOUL_DB must point to a file outside dev` |
| A 32-byte key exists (env or file) | `CRONSOUL_SECRET_KEY or CRONSOUL_SECRET_KEY_FILE is required` |

### Backups

Back up `/data` as a pair — the database **and** `secret.key`. A database without its key cannot decrypt saved notification credentials. Copying the raw `.db` file while CronSoul is running can capture a half-written state; stop the container, or use `sqlite3 cronsoul.db ".backup /tmp/cronsoul-backup.db"`.

### Updating

```bash
docker compose pull && docker compose up -d
```

Keep the same `/data` volume. Downgrading is not supported — the schema only moves forward, so restore a matching database backup if you roll the image back.

---

## 🚀 Build from source

```bash
git clone https://github.com/user4xn/cronsoul-public
cd cronsoul-public
go run ./cmd/cronsoul
```

Open [http://127.0.0.1:9470](http://127.0.0.1:9470). Data stays in memory, which is fine for a look around. To keep it on disk:

```bash
make dev-file        # or: CRONSOUL_DB=./cronsoul.db go run ./cmd/cronsoul
```

Build a static binary or a local image:

```bash
make build                                   # writes ./cronsoul
docker build -t cronsoul:local .
```

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

Everything is optional for a local run; the image already sets the first four. The **Production** column is what `CRONSOUL_ENV=production` requires.

| Variable | Default (image) | Production | Description |
|---|---|---|---|
| `CRONSOUL_ENV` | `dev` | `production` | Switches on startup validation. |
| `CRONSOUL_DB` | `/data/cronsoul.db` | required, file | SQLite path. `:memory:` is rejected outside dev. |
| `CRONSOUL_SECRET_KEY_FILE` | `/data/secret.key` | one of these | Key file, generated once on the volume and reused. |
| `CRONSOUL_SECRET_KEY` | _(unset)_ | one of these | Inline alternative: 32 bytes, raw or base64. |
| `CRONSOUL_TLS_MODE` | `off` | `proxy` or `cert` | `off` is dev-only. |
| `CRONSOUL_BASE_URL` | _(unset)_ | required | Public HTTPS URL, e.g. `https://cron.example.com`. |
| `CRONSOUL_TLS_CERT` | _(unset)_ | when `cert` | Path to `fullchain.pem`. |
| `CRONSOUL_TLS_KEY` | _(unset)_ | when `cert` | Path to unencrypted `privkey.pem`. |
| `CRONSOUL_LISTEN` | `:9470` | `:9470` | Listen address inside the container. |
| `CRONSOUL_DISPLAY_TZ` | `Asia/Jakarta` | `Asia/Jakarta` | Default console timezone; changeable in Settings. |
| `CRONSOUL_SESSION_TTL` | `12h` | `12h` | Session lifetime, Go duration syntax. |

`CRONSOUL_SECRET_KEY` and `CRONSOUL_SECRET_KEY_FILE` are alternatives — set one, not both. Full reference, proxy notes and backup details: [docs/CONTAINER.md](docs/CONTAINER.md).

---

## 🩺 Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| Job fails with *not allowlisted* | Destination host is not on the allowlist | Add the host under **Allowlist** |
| `CRONSOUL_TLS_MODE=off is allowed only in dev` | `CRONSOUL_ENV=production` with TLS off | Set `CRONSOUL_TLS_MODE=proxy` (or `cert`) |
| `CRONSOUL_SECRET_KEY ... is required` | Production without a key | Mount `/data/secret.key` or set `CRONSOUL_SECRET_KEY` |
| Container restarts with `permission denied` in `cert` mode | Let's Encrypt key is `600 root` | Run as root or fix ownership |
| Login works, console is blank | Stale browser cache after an upgrade | Hard reload; the UI is embedded in the binary |
| `409` when pressing Play | Singleton endpoint already running | Wait for the current run, or disable singleton |
| Notification test fails but channel looks configured | Credentials were encrypted with an older key | Re-enter the channel credentials |
| Everything lost after recreating the container | Volume not mounted | Always mount `/data` |

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

---

## 📦 Repository Scope

The public tree is the application source, tests, Docker and Compose files, CI/publish workflows, and the operator documentation under `docs/`. Credentials, databases, generated keys and deployment-specific configuration are not tracked; `.env.example` holds placeholders only.

Earlier drafts — mockups, PRDs, brand notes and internal research — were removed before publication, and the public repository starts from a single clean commit.

---

## 📚 Documentation

- [How to use CronSoul](docs/HOW-TO-USE.md) — every screen, end to end.
- [Container deployment](docs/CONTAINER.md) — environment, TLS, storage, backup, upgrades.
- [HTTP API](docs/openapi.yaml) — OpenAPI 3 description of the console's routes.
- [Database schema](docs/cronsoul-schema.sql) — reference; embedded migrations are authoritative.

---

## 📄 License

MIT — see [LICENSE](LICENSE). Use it, fork it, ship it.

---

Built by [@user4xn](https://github.com/user4xn).

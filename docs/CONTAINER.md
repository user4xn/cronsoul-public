# Container image and configuration

CronSoul is distributed as a self-hosted container image. The image registry is GHCR; the repository and package can have separate visibility settings. The container stores its SQLite database and encryption key under `/data`; keep that volume persistent and back it up.

- GHCR image: `ghcr.io/user4xn/cronsoul`
- Source: <https://github.com/user4xn/cronsoul>
- Image tags: `master` tracks the current `master` build; a reviewed `vX.Y.Z` version tag publishes both that tag and `latest`.
- Architectures: Linux amd64 and arm64 are built by the publish workflow.

For anonymous pulls, the GHCR package must be set to **Public** in the package settings. Making the source repository public does not automatically change the package's visibility. This repository does not currently point at a Docker Hub image; Docker Hub is an alternative if a public Docker Hub namespace is preferred later.

## Pull and run

For the current branch image:

```bash
docker pull ghcr.io/user4xn/cronsoul:master
```

For an approved stable release, prefer an immutable version tag:

```bash
docker pull ghcr.io/user4xn/cronsoul:vX.Y.Z
# `latest` is also published on version-tag releases
docker pull ghcr.io/user4xn/cronsoul:latest
```

The `latest` tag is not guaranteed to exist until the first approved `vX.Y.Z` tag is pushed. Start with the branch image for a local evaluation — no environment variables are needed:

```bash
docker volume create cronsoul-data
docker run -d --name cronsoul --restart unless-stopped \
  -p 127.0.0.1:9470:9470 \
  -v cronsoul-data:/data \
  ghcr.io/user4xn/cronsoul:master
```

The image ships with `CRONSOUL_ENV=dev`, `CRONSOUL_TLS_MODE=off`, `CRONSOUL_DB=/data/cronsoul.db` and `CRONSOUL_SECRET_KEY_FILE=/data/secret.key`. The key is generated once on the volume, so channel credentials stay readable across restarts. Supply the environment explicitly only when you move to production (see below).

Quote any `docker run` flags before the image name. `docker run image --name x` passes `--name x` to the container process instead of naming the container.

The image is also usable from Compose. The repository's `docker-compose.yml` provides a loopback-only development/evaluation setup and tracks the `master` image. For a stable deployment, change the image to a reviewed version tag, such as `v1.2.3`, before starting the stack.

Verify startup:

```bash
curl -fsS http://127.0.0.1:9470/healthz
```

Open <http://127.0.0.1:9470>. The first boot creates `admin` / `12345678` if the users table is empty, and forces a password change. Change it immediately. Do not reuse this credential or expose the dev configuration remotely.

## Environment variables

All names use the `CRONSOUL_` prefix. Values are set explicitly below; do not copy real secrets into shell history, tickets, CI logs, or public issues.

Defaults in the published image are `CRONSOUL_ENV=dev`, `CRONSOUL_TLS_MODE=off`, `CRONSOUL_DB=/data/cronsoul.db` and `CRONSOUL_SECRET_KEY_FILE=/data/secret.key`. A plain `docker run` therefore boots without extra flags; anything reachable beyond localhost should move to the production values below.

| Variable | Default | Purpose / production requirement |
|---|---|---|
| `CRONSOUL_ENV` | `dev` | Set `production` for production validation. |
| `CRONSOUL_LISTEN` | `:9470` | Address inside the container. Leave this at `:9470` when using published port mapping. |
| `CRONSOUL_DB` | `:memory:` in dev | Persistent SQLite path, normally `/data/cronsoul.db`. Non-dev must not use in-memory storage. |
| `CRONSOUL_SECRET_KEY_FILE` | unset | Preferred in containers; point to a persistent `/data` file. Generated once if absent and reused. Protect the volume. |
| `CRONSOUL_SECRET_KEY` | unset | Alternative key source. Must decode to exactly 32 bytes. Use a secret manager, not a committed env file. |
| `CRONSOUL_BASE_URL` | unset | Required outside dev; must be the public HTTPS URL, e.g. `https://cron.example.com`. |
| `CRONSOUL_TLS_MODE` | `off` | Production: `proxy` behind a TLS reverse proxy, or `cert` for TLS served directly by CronSoul. `off` is dev-only. |
| `CRONSOUL_TLS_CERT` | unset | Certificate path when `CRONSOUL_TLS_MODE=cert`. Mount certificate read-only. |
| `CRONSOUL_TLS_KEY` | unset | Private key path when `CRONSOUL_TLS_MODE=cert`. Mount read-only; never bake into the image. |
| `CRONSOUL_SESSION_TTL` | `12h` | Session duration in Go duration format, e.g. `8h`. |
| `CRONSOUL_DISPLAY_TZ` | `Asia/Jakarta` | Default UI display/schedule timezone. |

Production refuses unsafe/incomplete configuration: missing 32-byte encryption key, non-HTTPS base URL, TLS off, or an in-memory/empty DB path.

### Secret key handling

The encryption key protects notification channel credentials. Treat it as a peer to the database:

- Persist `/data/secret.key` and `/data/cronsoul.db` together; a DB backup without the matching key cannot decrypt saved channel configuration.
- Do not put the key in Dockerfile `ARG`, image layers, Git, issue trackers, or shared command output.
- Replacing the key does not re-encrypt old ciphertext. To rotate: back up the database, stop the container, put the new key in place, start it, then re-enter every notification channel credential and send a test message. Old encrypted values cannot be recovered.
- For production, set key file ownership/mode so only the container service identity can read it.

## Production behind a reverse proxy

Keep CronSoul bound to a private/container network and terminate TLS at Caddy, nginx, Traefik, or your platform ingress. Set the actual HTTPS URL and proxy mode:

```yaml
environment:
  CRONSOUL_ENV: production
  CRONSOUL_BASE_URL: https://cron.example.com
  CRONSOUL_TLS_MODE: proxy
  CRONSOUL_LISTEN: ":9470"
  CRONSOUL_DB: /data/cronsoul.db
  CRONSOUL_SECRET_KEY_FILE: /data/secret.key
```

Only the reverse proxy should be internet-facing. Do not publish `9470` publicly without TLS and an intentional network policy. Configure trusted proxy headers only according to your proxy setup; avoid sharing the admin panel on an untrusted network.

## Custom build

To build from a checked-out source tree instead of pulling the image:

```bash
docker build --pull -t cronsoul:local .
docker run --rm -p 127.0.0.1:9470:9470 -v cronsoul-data:/data \
  -e CRONSOUL_ENV=dev -e CRONSOUL_DB=/data/cronsoul.db \
  -e CRONSOUL_SECRET_KEY_FILE=/data/secret.key cronsoul:local
```

The image is multi-stage and contains a static Go binary plus CA roots and timezone data; there is no Node runtime, shell, or package manager in the final `scratch` image. SQLite database and keys are external persistent data, never image content.

## Upgrade and rollback

1. Read the release notes and choose an explicit version tag.
2. Back up the SQLite DB with a consistent SQLite backup and copy the encryption key securely.
3. Pull the selected image and recreate the container, keeping the existing `/data` volume.
4. Check `/healthz`, log in, and verify a read-only endpoint/run view.
5. Rollback by recreating the prior image tag with the same data volume. Downgrading DB schemas is not guaranteed; restore the matching DB backup if the old binary cannot read the migrated schema.

## Publish workflow

The GitHub Actions workflow `.github/workflows/publish-image.yml` publishes the `master` image on pushes to `master`; a version tag `vX.Y.Z` publishes that version and `latest`. Tag creation is a public release action; only create a version tag after the release is approved.

The workflow uses the repository-provided `GITHUB_TOKEN` and requires no registry password secret. After the first successful package publication, a maintainer can set GHCR package visibility to **Public** in GitHub Packages settings. Verify anonymous pull from a clean machine before announcing the image.

Use the immutable `vX.Y.Z` tag for deployments. `latest` is convenient for evaluation, but changes only on explicit version-tag releases.

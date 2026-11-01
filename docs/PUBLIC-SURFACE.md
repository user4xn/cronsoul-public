# Public repository surface audit

Maintainer: [@user4xn](https://github.com/user4xn).

This file records what the public tree contains, what was removed, and what still needs a decision before the repository is treated as a finished public surface.

## Current status

The GitHub repository **and** the `ghcr.io/user4xn/cronsoul` container package are both private at the time of this audit. Publishing takes two separate actions: changing repository visibility, and setting the GHCR package visibility to Public. Neither one implies the other.

## Public product surface (current tree)

These paths are needed to use, build, test, or operate CronSoul:

- `cmd/`, `internal/`, `examples/`, `go.mod`, `go.sum` — application, tests, and the receiver example.
- `Dockerfile`, `docker-compose.yml`, `.dockerignore`, `.gitignore`, `.env.example` — container/build files and placeholder configuration.
- `.github/workflows/` — CI and image publishing. Publishing uses the repository-provided `GITHUB_TOKEN` for GHCR; no registry secret is committed.
- `cmd/cronsoul/ui/` — embedded SPA, fonts, and runtime brand assets.
- `README.md`, `docs/HOW-TO-USE.md`, `docs/CONTAINER.md`, `docs/openapi.yaml`, `docs/cronsoul-schema.sql`, and this audit — operator, API, deployment, and schema references.
- `Makefile` — local `dev`, `build`, `test`, and `check` targets.

## Removed in the public-release cleanup

These were deleted from the tree and are not referenced by the current code or documentation:

- `archive/`, `ui/` — superseded mockups, draft product documents, and design screenshots.
- `docs/cronsoul-backlog.md`, `cronsoul-prd-fase1.md`, `cronsoul-concept.md`, `cronsoul-vs-alternatives.md`, `cronsoul-brand.md`, `cronsoul-ringkasan.md`, `cronsoul-openapi.yaml`, `cronsoul-tech-design.md` — planning, competitor, and internal design notes.
- `design/` — internal design rationale sheets.
- `research/` — internal market and pricing research that referenced an unrelated employer project.

The API contract was rewritten from the shipping routes and now lives at `docs/openapi.yaml`.

Removal from the tree is not removal from Git history. See the history warning below.

## Secret and configuration checks

- `.env.example` is the only tracked env file. `.env`, `.env.*` (except `.env.example`), `.secrets/`, SQLite files, key files, and certificates are covered by `.gitignore`.
- No tokens, private keys, passwords, or credentials are present in the tracked tree (checked for GitHub/OpenAI/AWS/Slack token shapes and PEM private-key headers).
- The seeded `admin` / `12345678` credential is documented on purpose: fresh instances create it and force a password change at first login. Keep that behaviour; never use it on an internet-exposed instance.
- Allowlist examples use reserved example hostnames, not real infrastructure.
- Do not commit real database snapshots, session cookies, bearer tokens, channel credentials, signing keys, TLS private keys, or production environment files.

## Visibility and history warning

This audit does not change repository visibility. Public visibility exposes every tracked file plus the reachable commit history, and a deleted file stays readable in history. Before flipping or mirroring the repository:

1. Scan the full history for secrets and internal documents (`git log -p`, or a dedicated secret scanner), not just the current tree.
2. Decide whether to publish a squashed history if earlier drafts must not be readable.
3. Confirm the GHCR package visibility separately — it is a package setting, not a repository setting.

## License status

There is no `LICENSE` file. Public visibility does not grant permission to reuse or redistribute the source. Choose a license before describing CronSoul as open source.

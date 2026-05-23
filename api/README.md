# Invosit API

The Go API server for Invosit.


## Requirements

- Go 1.26+
- Docker + Docker Compose
- Cloudflare R2 or AWS S3 bucket (More storage providers coming soon)


## Getting started

Compose-orchestrated services live at the repo root; the API itself is
built from this directory.

```bash
# from the repo root
cp .env.example .env # fill in storage credentials and Kratos secrets
docker compose up -d # start Postgres, Redis, Kratos

# then from this directory
cd api
go run ./cmd/server # start API on :8080
```

Compose brings up:

| Service | Host port | Purpose |
|---|---|---|
| `api` | `8080` | The Go API server |
| `kratos` | `4433` | Kratos public API (admin on `4434`, not host-exposed). Migrations idempotent. |
| `postgres` | `5432` | Hosts both `invosit` and `kratos` databases |
| `redis` | `6379` | Provisioned for planned rate limiting; not yet wired into the API process |
| `frontend` | `5173` | Invosit frontend + dashboard |

Migrations run automatically on startup.


## Commands

All Go commands run from this directory (that's where `go.mod` lives):

```bash
go build ./...
go vet ./...
go test -race -timeout 5m ./...
golangci-lint run --timeout=5m # config: .golangci.yml
```

CI runs all four on every PR (with `working-directory: api`), plus
`govulncheck` and `gitleaks`.


## Configuration

All config via environment variables.  `.env` lives at the repo root and is
consumed by both `docker compose` and the API process.

```bash
# Server
PORT=8080

# Ory Kratos — set all four secrets per Ory's production guide
# https://www.ory.com/docs/kratos/guides/production
KRATOS_PUBLIC_URL=http://kratos:4433
KRATOS_WEBHOOK_SECRET=           # openssl rand -hex 32

# Kratos itself (consumed by the kratos container)
KRATOS_DEFAULT_SECRET=           # openssl rand -hex 32
KRATOS_COOKIE_SECRET=            # openssl rand -hex 32
KRATOS_CIPHER_SECRET=            # exactly 32 chars

# Postgres (single container, two logical databases: invosit + kratos)
DATABASE_URL=postgres://invosit:secret@localhost:5432/invosit

# Storage — pick one provider
STORAGE_PROVIDER=r2
STORAGE_BUCKET=invosit-blobs
STORAGE_ACCESS_KEY=
STORAGE_SECRET_KEY=
STORAGE_ENDPOINT=
STORAGE_REGION=auto

# CORS
CORS_ALLOWED_ORIGINS=http://localhost:3000
```

### Storage providers

**Cloudflare R2** (recommended — zero egress fees):
```bash
STORAGE_PROVIDER=r2
STORAGE_ENDPOINT=https://<accountid>.r2.cloudflarestorage.com
STORAGE_BUCKET=invosit-blobs
STORAGE_ACCESS_KEY=xxx
STORAGE_SECRET_KEY=xxx
STORAGE_REGION=auto
```

**AWS S3:**
```bash
STORAGE_PROVIDER=s3
STORAGE_REGION=us-east-1
STORAGE_BUCKET=invosit-blobs
STORAGE_ACCESS_KEY=xxx
STORAGE_SECRET_KEY=xxx
```


## Security model

The same security rules apply across every component in this repo.

### The server never sees plaintext

Files are encrypted client-side by the CLI before upload. The API only
ever handles encrypted blobs and opaque wrapped DEKs. A full server
compromise does not expose file contents. The web frontend sits in the
same trust position as the API — it must never decrypt file content
either.

### Access control is cryptographic

Every file has a `wrapped_dek` row per authorised user — a DEK encrypted
with that user's public key. No row means no access, enforced at the
crypto layer, not just the permission check layer. Revoking a user
deletes their wrapped DEK rows; their next pull returns 403 instantly.
No re-encryption needed.

### Authentication

Identity, registration, login, password hashing (Argon2id), session
lifecycle, recovery, and email verification are all delegated to **Ory
Kratos**. The API validates incoming requests by calling Kratos
`/sessions/whoami` — there is no JWT or refresh token rotation in this
codebase. When Kratos creates a new identity, it fires an after-registration
webhook that creates the corresponding `users` row locally (gated by a
shared-secret header, constant-time compared).

The CLI uses Kratos's native API flow (Bearer tokens). The web frontend
will use Kratos's browser flow (cookies). Both land at the same
`/sessions/whoami` validation server-side.

### Rate limiting (planned, not yet wired)

When implemented, will be Redis-backed (Redis is already in compose):
- API endpoints: 300 requests/minute per IP
- File push: 60 requests/minute per IP

Login / registration brute-force is already rate-limited by Kratos itself
(configured in `api/kratos/kratos.yml`).

### Signed storage URLs

Download URLs expire in 15 minutes and are only issued after auth and
wrapped-DEK verification pass. The server never proxies file content.

### Security headers (planned, not yet wired)

When implemented, every API response will include:
```
X-Content-Type-Options: nosniff
X-Frame-Options: DENY
Strict-Transport-Security: max-age=31536000; includeSubDomains
Content-Security-Policy: default-src 'none'
Referrer-Policy: no-referrer
```


## API surface

Current base URL: `/api/v1`


View the full schema: [`../docs/openapi.yaml`](../docs/openapi.yaml)


## Layout

```
api/
├── cmd/server/             # entry point, route registration
├── internal/
│   ├── kratos/             # thin Kratos client (whoami)
│   ├── handler/            # net/http handlers, one file per resource group
│   ├── middleware/         # Recovery, CORS, Logger, BodyLimit, Kratos session, workspace + environment scoping (rate limiting + security headers planned)
│   ├── httpx/              # JSON bind/respond helpers, request ctx
│   ├── ids/                # prefixed ID generator
│   ├── db/                 # Postgres connection helpers
│   └── storage/            # storage interface + R2/S3 (GCS planned)
├── kratos/                 # Kratos config (kratos.yml, identity schema, hooks)
├── db/init/                # Postgres init scripts (CREATE DATABASE kratos)
├── migrations/             # SQL migration files
├── go.mod
├── Dockerfile
└── .golangci.yml
```


## Deployment

The API runs as a Docker container behind Nginx Proxy Manager on a VPS.

```bash
# On your VPS
docker compose -f docker-compose.prod.yml pull
docker compose -f docker-compose.prod.yml up -d
```

### Calibrate Argon2 on the target host

The Argon2 parameters in `kratos/kratos.yml` are MVP starter values, not
calibrated for the target hardware. Per [Ory's guidance](https://www.ory.com/docs/kratos/guides/setting-up-password-hashing-parameters),
run on the production VPS and replace the values:

```bash
docker compose run --rm kratos hashers argon2 calibrate 1s
```

Aim for ~0.5–1s per hash. Too fast weakens the hash; too slow opens DoS
on login. Re-run whenever the VPS's hardware changes.

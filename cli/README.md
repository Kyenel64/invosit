# Invosit CLI

Go CLI for Invosit. 

## Commands

- `invosit login` — opens a browser to sign in (GitHub OIDC).
- `invosit user get` — prints the saved user id and email.
- `invosit init` — binds the current directory to a workspace + environment via an interactive picker, writing `.invosit.json`.

## Dev

Bring the API + Kratos stack up first (from the monorepo root):

```bash
docker compose up -d postgres redis kratos api
cd frontend && npm install && npm run dev   # serves the login page at :5173
```

Then from `cli/`:

```bash
go build -o invosit .
./invosit login
./invosit user get
```

`go test ./...`, `go vet ./...`, and `golangci-lint run` all run from
inside `cli/` (its own `go.mod`).

## Layout

- `cmd/` — Cobra commands. One file per command group.
- `internal/kratos/` — thin wrapper around Ory's `client-go` SDK plus the
  native-app browser-login dance (loopback listener +
  `return_session_token_exchange_code` exchange).
- `internal/credstore/` — credential persistence (`0600`, atomic write,
  perm check on load).
- `internal/apiclient/` — typed client for the Invosit API. Calls
  `/auth/me`, `/workspaces`, and `/workspaces/{id}/environments`.
- `internal/config/` — `.invosit.json` Load/Save/Validate (atomic
  write, JSON decode with unknown-field rejection).
- `internal/tui/` — Bubbletea interactive picker used by `invosit init`.

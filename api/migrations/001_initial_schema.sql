CREATE TABLE users (
    id                 TEXT PRIMARY KEY,
    email              TEXT NOT NULL,
    kratos_identity_id UUID NOT NULL UNIQUE,
    public_key         TEXT,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX users_email_lower ON users ((LOWER(email)));

CREATE TABLE workspaces (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    created_by TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE workspace_members (
    workspace_id TEXT        NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    user_id      TEXT        NOT NULL REFERENCES users(id)      ON DELETE CASCADE,
    role         TEXT        NOT NULL CHECK (role IN ('admin', 'member', 'viewer', 'no_access')),
    joined_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at   TIMESTAMPTZ,
    PRIMARY KEY (workspace_id, user_id)
);

CREATE INDEX idx_workspace_members_user ON workspace_members(user_id);

CREATE TABLE environments (
    id           TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL        REFERENCES workspaces(id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX environments_workspace_name_lower ON environments (workspace_id, LOWER(name));

CREATE TABLE files (
    id             TEXT        PRIMARY KEY,
    workspace_id   TEXT        NOT NULL REFERENCES workspaces(id)   ON DELETE CASCADE,
    environment_id TEXT        NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    path           TEXT        NOT NULL,
    content_hash   TEXT        NOT NULL,
    size           BIGINT      NOT NULL,
    version        INTEGER     NOT NULL DEFAULT 1, -- optimistic-concurrency counter, +1 per overwrite; the push CAS token (see #79)
    pushed_by      TEXT                 REFERENCES users(id)        ON DELETE SET NULL,
    pushed_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX        idx_files_workspace ON files (workspace_id);
CREATE UNIQUE INDEX files_env_path      ON files (environment_id, path);

CREATE TABLE pending_files (
    id                TEXT        PRIMARY KEY,
    workspace_id      TEXT        NOT NULL REFERENCES workspaces(id)   ON DELETE CASCADE,
    environment_id    TEXT        NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    path              TEXT        NOT NULL,
    content_hash      TEXT        NOT NULL,
    size              BIGINT      NOT NULL,
    expected_version  INTEGER     NOT NULL, -- base version the push declared (0 = create)
    pushed_by         TEXT                 REFERENCES users(id)        ON DELETE SET NULL,
    pushed_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_file_id TEXT                 REFERENCES files(id)        ON DELETE SET NULL
);

CREATE INDEX idx_pending_files_pushed_at ON pending_files (pushed_at);

CREATE TABLE wrapped_deks (
    file_id       TEXT  NOT NULL REFERENCES files(id) ON DELETE CASCADE,
    user_id       TEXT  NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    encrypted_dek BYTEA NOT NULL,
    PRIMARY KEY (file_id, user_id)
);

CREATE INDEX idx_wrapped_deks_user ON wrapped_deks (user_id);

CREATE TABLE access_grants (
    id             TEXT        PRIMARY KEY,
    user_id        TEXT        NOT NULL REFERENCES users(id)        ON DELETE CASCADE,
    workspace_id   TEXT        NOT NULL REFERENCES workspaces(id)   ON DELETE CASCADE,
    environment_id TEXT                 REFERENCES environments(id) ON DELETE CASCADE,
    path_pattern   TEXT        NOT NULL,
    granted_by     TEXT                 REFERENCES users(id)        ON DELETE SET NULL,
    granted_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_access_grants_user_workspace ON access_grants (user_id, workspace_id);

CREATE TABLE audit_logs (
    id           TEXT        PRIMARY KEY,
    user_id      TEXT                 REFERENCES users(id)      ON DELETE SET NULL,
    workspace_id TEXT                 REFERENCES workspaces(id) ON DELETE CASCADE,
    action       TEXT        NOT NULL,
    file_id      TEXT                 REFERENCES files(id)      ON DELETE SET NULL,
    ip           TEXT,
    timestamp    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_audit_logs_workspace_ts ON audit_logs (workspace_id, timestamp DESC);

CREATE TABLE admin_users (
    id TEXT PRIMARY KEY,
    username TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    password_changed_at TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE sessions (
    id TEXT PRIMARY KEY,
    admin_user_id TEXT NOT NULL REFERENCES admin_users(id) ON DELETE CASCADE,
    session_token_hash TEXT NOT NULL UNIQUE,
    csrf_token_hash TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    last_seen_at TEXT NOT NULL,
    created_at TEXT NOT NULL,
    ip_hash TEXT,
    user_agent TEXT NOT NULL DEFAULT ''
);

CREATE INDEX sessions_admin_user_id_idx ON sessions(admin_user_id);
CREATE INDEX sessions_expires_at_idx ON sessions(expires_at);

CREATE TABLE settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE listeners (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    enabled INTEGER NOT NULL CHECK (enabled IN (0, 1)),
    listen_address TEXT NOT NULL,
    listen_port INTEGER NOT NULL UNIQUE CHECK (listen_port BETWEEN 1 AND 65535),
    public_host_override TEXT,
    public_port_override INTEGER CHECK (
        public_port_override IS NULL
        OR public_port_override BETWEEN 1 AND 65535
    ),
    server_name TEXT NOT NULL,
    reality_dest TEXT NOT NULL,
    reality_private_key_ciphertext TEXT NOT NULL,
    reality_public_key TEXT NOT NULL,
    short_id TEXT NOT NULL,
    udp_enabled INTEGER NOT NULL CHECK (udp_enabled IN (0, 1)),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE listener_users (
    id TEXT PRIMARY KEY,
    listener_id TEXT NOT NULL REFERENCES listeners(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    enabled INTEGER NOT NULL CHECK (enabled IN (0, 1)),
    uuid TEXT NOT NULL,
    expires_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE(listener_id, name),
    UNIQUE(listener_id, uuid)
);

CREATE INDEX listener_users_listener_id_idx ON listener_users(listener_id);
CREATE INDEX listener_users_expires_at_idx ON listener_users(expires_at);

CREATE TABLE config_revisions (
    id TEXT PRIMARY KEY,
    revision_number INTEGER NOT NULL UNIQUE,
    sha256 TEXT NOT NULL,
    file_path TEXT NOT NULL,
    state_file_path TEXT NOT NULL,
    status TEXT NOT NULL CHECK (
        status IN ('pending', 'active', 'failed', 'rolled_back')
    ),
    reason TEXT NOT NULL,
    actor_admin_id TEXT REFERENCES admin_users(id) ON DELETE SET NULL,
    error_message_redacted TEXT,
    created_at TEXT NOT NULL,
    activated_at TEXT
);

CREATE INDEX config_revisions_status_idx ON config_revisions(status);
CREATE INDEX config_revisions_created_at_idx ON config_revisions(created_at);

CREATE TABLE audit_logs (
    id TEXT PRIMARY KEY,
    actor_admin_id TEXT REFERENCES admin_users(id) ON DELETE SET NULL,
    action TEXT NOT NULL,
    resource_type TEXT NOT NULL,
    resource_id TEXT,
    result TEXT NOT NULL CHECK (result IN ('success', 'failure')),
    summary_redacted TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE INDEX audit_logs_created_at_idx ON audit_logs(created_at);
CREATE INDEX audit_logs_action_idx ON audit_logs(action);

CREATE TABLE system_state (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    degraded INTEGER NOT NULL CHECK (degraded IN (0, 1)),
    degraded_reason TEXT NOT NULL DEFAULT '',
    degraded_revision_id TEXT,
    updated_at TEXT NOT NULL
);

INSERT INTO system_state (
    id,
    degraded,
    degraded_reason,
    degraded_revision_id,
    updated_at
) VALUES (1, 0, '', NULL, '1970-01-01T00:00:00Z');

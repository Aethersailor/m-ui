-- Development-stage cutover for Node Schema V2 protocol expansion.
-- Existing nodes use encryption purposes and CHECK constraints from the
-- pre-R3 schema, so retaining them would be unsafe and misleading. The user
-- explicitly permits dropping development node configuration/history.
DELETE FROM config_revisions;
DELETE FROM node_users;
DELETE FROM access_profiles;
DELETE FROM nodes;

DROP TABLE node_users;
DROP TABLE access_profiles;
DROP TABLE nodes;

CREATE TABLE nodes (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    enabled INTEGER NOT NULL CHECK (enabled IN (0, 1)),
    listen_address TEXT NOT NULL,
    port TEXT NOT NULL,
    protocol_kind TEXT NOT NULL CHECK (protocol_kind IN ('vless', 'hysteria2', 'vmess', 'trojan', 'shadowsocks')),
    protocol_schema_version INTEGER NOT NULL CHECK (protocol_schema_version > 0),
    protocol_config_json TEXT NOT NULL,
    protocol_secret_ciphertext TEXT NOT NULL,
    generation INTEGER NOT NULL CHECK (generation > 0),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE UNIQUE INDEX nodes_bind_idx ON nodes(listen_address, port);
CREATE INDEX nodes_protocol_idx ON nodes(protocol_kind);

CREATE TABLE node_users (
    id TEXT PRIMARY KEY,
    node_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    enabled INTEGER NOT NULL CHECK (enabled IN (0, 1)),
    credential_kind TEXT NOT NULL CHECK (credential_kind IN ('vless', 'hysteria2', 'vmess', 'trojan', 'shadowsocks')),
    credential_ciphertext TEXT NOT NULL,
    expires_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE(node_id, name)
);

CREATE INDEX node_users_node_id_idx ON node_users(node_id);
CREATE INDEX node_users_expires_at_idx ON node_users(expires_at);

CREATE TABLE access_profiles (
    id TEXT PRIMARY KEY,
    node_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    is_default INTEGER NOT NULL CHECK (is_default IN (0, 1)),
    public_host TEXT NOT NULL DEFAULT '',
    public_port INTEGER NOT NULL CHECK (public_port BETWEEN 1 AND 65535),
    server_name TEXT NOT NULL DEFAULT '',
    fingerprint TEXT NOT NULL DEFAULT '',
    packet_encoding TEXT NOT NULL DEFAULT '',
    allow_insecure INTEGER NOT NULL CHECK (allow_insecure IN (0, 1)),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE(node_id, name)
);

CREATE UNIQUE INDEX access_profiles_one_default_idx
    ON access_profiles(node_id)
    WHERE is_default = 1;
CREATE INDEX access_profiles_node_id_idx ON access_profiles(node_id);

CREATE TABLE core_settings (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    channel TEXT NOT NULL CHECK (channel IN ('release', 'alpha')),
    auto_update INTEGER NOT NULL CHECK (auto_update IN (0, 1)),
    check_interval_seconds INTEGER NOT NULL CHECK (
        check_interval_seconds IN (21600, 43200, 86400, 604800)
    ),
    managed INTEGER NOT NULL CHECK (managed IN (0, 1)),
    external_path TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL
);

CREATE TABLE core_state (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    current_manifest_json TEXT,
    available_identity_json TEXT,
    last_check_at TEXT,
    last_check_result TEXT NOT NULL DEFAULT '',
    last_update_at TEXT,
    last_update_result TEXT NOT NULL DEFAULT '',
    last_error_redacted TEXT NOT NULL DEFAULT '',
    next_check_at TEXT,
    update_in_progress INTEGER NOT NULL DEFAULT 0 CHECK (
        update_in_progress IN (0, 1)
    )
);

INSERT INTO core_state(id, update_in_progress) VALUES (1, 0);

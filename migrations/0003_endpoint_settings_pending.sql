CREATE TABLE endpoint_settings_pending (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    panel_ui_bind_host TEXT NOT NULL,
    panel_ui_bind_port INTEGER NOT NULL CHECK (
        panel_ui_bind_port BETWEEN 1 AND 65535
    ),
    mihomo_external_controller_bind_host TEXT NOT NULL,
    mihomo_external_controller_bind_port INTEGER NOT NULL CHECK (
        mihomo_external_controller_bind_port BETWEEN 1 AND 65535
    ),
    mihomo_controller_connect_host TEXT NOT NULL,
    mihomo_controller_connect_port INTEGER NOT NULL CHECK (
        mihomo_controller_connect_port BETWEEN 1 AND 65535
    ),
    external_controller_cors_origins_json TEXT NOT NULL,
    generation INTEGER NOT NULL CHECK (generation > 0),
    requires_mui_restart INTEGER NOT NULL CHECK (requires_mui_restart IN (0, 1)),
    requires_mihomo_restart INTEGER NOT NULL CHECK (requires_mihomo_restart IN (0, 1)),
    updated_at TEXT NOT NULL
);

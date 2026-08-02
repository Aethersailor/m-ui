CREATE TABLE endpoint_settings_last_applied (
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
    updated_at TEXT NOT NULL
);

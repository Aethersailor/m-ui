CREATE TABLE bootstrap_state (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    token_hash TEXT NOT NULL DEFAULT '',
    token_ciphertext TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    rotated_at TEXT,
    consumed_at TEXT,
    CHECK (
        (consumed_at IS NULL AND token_hash <> '' AND token_ciphertext <> '') OR
        (consumed_at IS NOT NULL AND token_hash = '' AND token_ciphertext = '')
    )
);

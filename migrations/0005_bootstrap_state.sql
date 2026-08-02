CREATE TABLE bootstrap_state (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    token_hash TEXT NOT NULL DEFAULT '',
    token_ciphertext TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    rotated_at TEXT,
    consumed_at TEXT
);

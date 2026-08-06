-- 0007 removed the old desired state and revision history, but already
-- deployed development databases can still have the pre-cutover config.yaml
-- loaded by Mihomo.  A later migration is required so those databases also
-- enter the durable convergence flow instead of relying on edited history.
-- Development data created after an earlier 0007 deployment is intentionally
-- cleared again so every pre-0008 state converges automatically to the same
-- empty baseline instead of requiring manual database repair.
DELETE FROM config_revisions;
DELETE FROM node_users;
DELETE FROM access_profiles;
DELETE FROM nodes;

CREATE TABLE runtime_convergence_state (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    r3_cutover_pending INTEGER NOT NULL CHECK (r3_cutover_pending IN (0, 1)),
    pending_reason TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL
);

INSERT INTO runtime_convergence_state (
    id,
    r3_cutover_pending,
    pending_reason,
    updated_at
) VALUES (
    1,
    1,
    'r3_protocol_cutover',
    '1970-01-01T00:00:00Z'
);

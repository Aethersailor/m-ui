# Configuration lifecycle

## Sources and artifacts

SQLite is the authoritative desired state for m-ui-managed settings,
listeners, users, and revisions. `/etc/mihomo/config.yaml` is a deterministic
artifact generated from that state. Editing it by hand is unsupported because
the next successful mutation replaces it.

The compiler:

1. takes one UTC effective-time snapshot;
2. filters disabled and expired records;
3. sorts listeners and users deterministically;
4. emits typed YAML for the supported v0.1 surface;
5. hashes the exact bytes with SHA-256.

No business state is assembled with untyped YAML maps.

## Publication transaction

All changes pass through one process-wide publisher lock and one SQLite
`BEGIN IMMEDIATE` transaction:

1. load and mutate desired state;
2. compile a candidate beside the active configuration;
3. fsync the candidate;
4. run real `mihomo -t -f <candidate>` with fixed arguments;
5. preserve the prior active YAML and structured state;
6. atomically replace the active YAML and fsync its directory;
7. reload through the authenticated loopback Controller, with the documented
   systemd restart fallback;
8. verify systemd and Controller health;
9. activate the revision and commit SQLite.

If a step fails, m-ui restores both the file and structured state. If automatic
recovery itself fails, it records degraded mode and blocks further publication
until an operator investigates.

## Revisions and rollback

Revision YAML and state snapshots live under `/var/lib/m-ui/revisions`, mode
`0600`. A rollback is another complete publication transaction: it restores
the selected structured state, recompiles it, validates it with the current
Mihomo binary, publishes it, and records a new active revision. The database
and active YAML therefore move together.

The configured history limit applies only to inactive revisions. Active
artifacts are never removed by retention cleanup.

## Expiry

The scheduler scans every 60 seconds. A batch uses one fixed UTC time,
disables every enabled user expired at that time, and auto-disables only
listeners affected by that batch whose resulting effective user set is empty.
It publishes once and records aggregate audit counts. Failure rolls back the
entire batch, which is retried by the next scan.

Adding a user never automatically re-enables a listener, and the expiry job
does not repair unrelated listener state.

## Backup and restore

For a cold backup:

```sh
sudo systemctl stop m-ui mihomo
sudo tar -C / -czf m-ui-backup.tar.gz \
  etc/m-ui etc/mihomo var/lib/m-ui
sudo systemctl start mihomo m-ui
```

Store the archive offline with restrictive permissions. The database and
`master.key` must remain paired.

For restore, use a clean supported host, stop both services, restore ownership
and modes, validate `/etc/mihomo/config.yaml` with `mihomo -t`, then start
Mihomo before m-ui. Check both journals and compare the active revision in the
panel before accepting traffic.

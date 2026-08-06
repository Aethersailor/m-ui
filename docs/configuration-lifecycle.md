# Configuration lifecycle

## Sources and artifacts

SQLite is the authoritative desired state for m-ui-managed settings,
nodes, protocol-owned specifications, users, access profiles, and revisions.
`/etc/mihomo/config.yaml` is a deterministic
artifact generated from that state. Editing it by hand is unsupported because
the next successful mutation replaces it.

The compiler:

1. takes one UTC effective-time snapshot;
2. filters disabled and expired records;
3. sorts enabled nodes deterministically and filters ineffective users;
4. dispatches each node through the compile-time protocol registry and emits
   typed VLESS or Hysteria2 YAML;
5. hashes the exact bytes with SHA-256.

The top-level compiler and protocol modules use explicit YAML output types.
Maps are used only where Mihomo's source contract itself is keyed (for example,
Hysteria2 username/password mappings).

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

`COMMIT` returning an error does not establish whether SQLite committed. The
publisher treats the original transaction as closed and uses an independent,
bounded recovery context to read and compile fresh durable state. It then
compares the durable database hash, unique active revision, and active YAML
with the saved old and candidate identities:

- database new and YAML new: accept the durable publication;
- database old and YAML new: restore and reload the old YAML, then report the
  commit failure;
- database new and YAML old or missing: validate and atomically republish the
  durable database result;
- database old and YAML old: report a clean commit failure;
- any other combination: enter degraded mode without overwriting either
  direction.

A failed revision is recorded only after this classification proves that the
candidate did not become the durable active revision.

Before every later mutation, the publisher repeats the active identity check
inside the publication transaction. It compiles database state at the fixed
`State.AsOf` stored in the active revision JSON and requires that digest, the
revision record, the archived revision YAML, and the current Mihomo YAML to
agree. A missing or externally changed active YAML blocks the mutation, is
left untouched, and marks the system degraded for startup reconciliation or
manual recovery.

## Startup reconciliation

Before the HTTP server, runtime monitor, or expiry scheduler starts, m-ui checks
the database, active revision metadata, revision YAML, revision JSON state
snapshot, and active Mihomo YAML. Revision JSON is decoded strictly and
recompiled at its fixed effective time, so wall-clock advancement alone does
not look like persistent corruption.

When the database, revision metadata, and both revision artifacts agree, a
missing or changed active YAML is rebuilt, validated with real `mihomo -t`,
atomically published, reloaded, and health checked. Database/revision
disagreement or a missing, changed, or invalid revision artifact enters
degraded mode. The panel and read-only runtime monitor continue to start, but
the expiry scheduler is not started and publisher mutations return the existing
degraded error.

No active revision is the valid initial bootstrap state. Startup does not
rewrite or validate the bootstrap YAML and does not mark that state degraded.
Failure to open or read the database remains a startup failure.

If reconciliation determines that degraded state is required, the persisted
degraded marker is part of the safety boundary. A failed attempt to clear or
write that marker is retried with a fresh bounded compensation context. If the
compensation write also fails, startup returns a fatal error that is not
classified as the ordinary persisted-degraded result, and no HTTP server or
background runner is started.

## Mihomo core lifecycle

Core settings and observed state are stored in SQLite; verified binaries and
manifests live below `/var/lib/m-ui/core`. The configured external binary path
is preserved during migration. A managed installation either bootstraps the
release-packaged core or explicitly adopts the existing local binary once.

For an update, m-ui:

1. resolves a release or rolling alpha identity from the fixed
   `MetaCubeX/mihomo` GitHub API endpoint;
2. selects exactly one `linux/amd64-compatible` or `linux/arm64` gzip asset;
3. requires the API-provided SHA-256 digest and a bounded declared size;
4. streams to same-filesystem staging with a bounded response and verifies the
   compressed digest before decompression;
5. verifies regular-file type, owner, mode, decompressed size and binary hash;
6. runs the candidate for its actual version and `-t -f` validation;
7. atomically activates it, restarts Mihomo, and checks both process and
   authenticated Controller health;
8. commits the manifest/state and retains at most two verified backups.

On any post-activation failure, the previous binary is restored and restarted
with a fresh bounded recovery context. If that recovery fails, durable degraded
state blocks later updates and publications. Startup removes interrupted
staging directories, revalidates the current manifest against the binary, and
clears stale in-progress flags; it never treats a tag string alone as proof of
the installed core.

Configuration publication, core check/update/rollback, and manual runtime
actions share one cancellable coordinator. A conflicting manual request fails
with a stable busy response, while schedulers retry with bounded backoff.

## Revisions and rollback

Revision YAML and state snapshots live under `/var/lib/m-ui/revisions`, mode
`0600`. A rollback is another complete publication transaction: it restores
the selected structured state, recompiles it, validates it with the current
Mihomo binary, publishes it, and records a new active revision. The database
and active YAML therefore move together.

The configured history limit is the number of inactive revisions to retain.
Inactive includes both `rolled_back` and `failed` revisions. Cleanup keeps the
newest inactive revisions in stable revision-number order; active artifacts are
never removed. Files are removed before their database row, so a file-removal
failure leaves the row intact. Cleanup runs as post-commit maintenance: its
failure is logged but does not turn an already committed publication into an
API error. The same best-effort maintenance runs after a failed revision is
successfully recorded; maintenance failure never replaces the original
publication error.

## Expiry

The scheduler scans every 60 seconds. A batch uses one fixed UTC time,
disables every enabled user expired at that time, and auto-disables only
nodes affected by that batch whose resulting effective user set is empty.
It publishes once and records aggregate audit counts. Failure rolls back the
entire batch, which is retried by the next scan.

Adding a user never automatically re-enables a node, and the expiry job does
not repair unrelated node state.

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

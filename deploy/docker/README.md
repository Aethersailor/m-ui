# Docker deployment

The image contains m-ui and a build-time verified Mihomo bootstrap. The
long-running m-ui process runs as UID/GID `10001:10001`, supervises Mihomo
directly, and never runs systemd or OpenRC in the container.

Choose one host directory. The default is `/opt/m-ui`; change it only by
setting `M_UI_DATA_DIR`:

```sh
export M_UI_DATA_DIR=/opt/m-ui
export M_UI_IMAGE_TAG=v0.1.0
docker compose -f deploy/docker/compose.yml up -d
docker compose -f deploy/docker/compose.yml ps
```

Compose creates this deterministic layout and maps it to the paths already
used by m-ui:

```text
/opt/m-ui/
├── etc/m-ui/       -> /etc/m-ui
├── etc/mihomo/     -> /etc/mihomo
├── var/lib/m-ui/   -> /var/lib/m-ui
└── var/lib/mihomo/ -> /var/lib/mihomo
```

The one-shot `data-init` service prepares ownership and exits before the
non-root main service starts. It has no network access. No administrator
password file, environment variable, Docker Secret, or generated password is
needed.

The panel listens on host loopback `127.0.0.1:2095`. Create the first
administrator in the Web UI through an SSH tunnel:

```sh
ssh -N -L 2095:127.0.0.1:2095 user@server
docker compose -f deploy/docker/compose.yml exec m-ui \
  m-ui admin setup-link --config /etc/m-ui/config.toml
```

Open the printed link in the local browser. Its one-time capability is in the
URL fragment, is consumed by the setup page, and is not stored in browser
storage or sent in the HTTP request URL. If the link is disclosed before use,
rotate it with `m-ui admin rotate-setup-token` through the same local command
path. CLI `reset-password` remains a recovery operation for an existing
administrator and cannot create the first administrator.

Use an immutable release or `sha-<12 hex>` tag for production. `edge` is a
master snapshot. Host networking is required because dynamically created
Mihomo Listeners may bind arbitrary host ports; keep the panel protected.
The image needs only `CAP_NET_BIND_SERVICE` for low Listener ports and must
not use `--privileged`.

To update:

```sh
export M_UI_IMAGE_TAG=v0.1.1
docker compose -f deploy/docker/compose.yml pull
docker compose -f deploy/docker/compose.yml up -d
```

The database, master key, generated configuration, revisions, and managed core
remain in the selected root. Back up all four subdirectories as one stopped
consistency set. Verify that the backup contains
`var/lib/m-ui/master.key` and `var/lib/m-ui/m-ui.db` when the instance has
already been initialized.

## Migrating legacy named volumes

Do not point the new Compose file at empty directories while an old named
volume deployment still contains data. Stop the old project, identify its
Compose project name, and run a dry run first:

```sh
docker compose -p old-m-ui -f deploy/docker/compose.legacy.yml down
deploy/docker/migrate-volumes.sh \
  --source-project old-m-ui \
  --target /opt/m-ui \
  --dry-run
deploy/docker/migrate-volumes.sh \
  --source-project old-m-ui \
  --target /opt/m-ui \
  --yes
```

The migration refuses a non-empty target, copies metadata into a staging
directory, verifies the expected four directories and the database/master-key
pair, and leaves every source volume untouched. It never removes a volume;
retain the sources until the new deployment has passed its health and login
checks.

The old named-volume shape is retained in `compose.legacy.yml` only for this
migration and rollback path.

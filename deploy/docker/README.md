# Docker deployment

The image contains m-ui and one build-time verified Mihomo release bootstrap.
m-ui runs as UID/GID `10001`, supervises Mihomo directly, and never runs
systemd or OpenRC in the container.

The Compose example uses host networking so dynamically created Mihomo
Listeners can bind arbitrary host ports. Host networking removes Docker port
isolation; keep the panel protected and use the default loopback/native reverse
proxy guidance where appropriate. The Controller remains bound to the
container network namespace loopback address.

Create `admin-password.txt` with a strong initial password, then run:

```sh
export M_UI_IMAGE_TAG=v0.1.0
docker compose up -d
docker compose ps
docker inspect --format '{{json .State.Health}}' m-ui
```

Use an immutable release or `sha-<12 hex>` tag for production. `edge` is a
master snapshot. The panel binds host loopback `127.0.0.1:2095`; use an SSH
tunnel or a separately managed HTTPS reverse proxy.

Persistent state is split across `/etc/m-ui`, `/etc/mihomo`,
`/var/lib/m-ui`, and `/var/lib/mihomo`. A newer image never overwrites an
already managed core or database in those volumes. The container needs only
`CAP_NET_BIND_SERVICE` for low Listener ports and must not use `--privileged`.

To update, pull the selected image and recreate the container:

```sh
export M_UI_IMAGE_TAG=v0.1.1
docker compose pull
docker compose up -d
docker compose ps
```

The existing database, master key, generated configuration, revisions and
managed core remain in the named volumes. Core updates performed from the panel
are independent from image replacement; a new image bootstrap is adopted only
when the core volume is empty.

Back up all four volumes as one stopped consistency set. Before removing the
container or host, verify that the backup contains `/var/lib/m-ui/master.key`
and `m-ui.db`. Removing the container does not remove named volumes; avoid
`docker compose down --volumes` unless a deliberate, separately confirmed purge
is intended.

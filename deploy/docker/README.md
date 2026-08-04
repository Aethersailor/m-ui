# Docker deployment

The image includes m-ui and a verified Mihomo bootstrap. Both long-running
processes use UID/GID `10001:10001`; the container never starts a root
initializer. The default and every formal Release Compose file use
`ghcr.io/aethersailor/m-ui:latest`.

## Quick start

With Docker Engine and Docker Compose v2 installed:

```sh
sudo install -d -o root -g root -m 0755 /opt/m-ui
sudo install -d -o 10001 -g 10001 -m 0700 /opt/m-ui/data
cd /opt/m-ui
sudo curl -fsSLo compose.yml https://raw.githubusercontent.com/Aethersailor/m-ui/master/deploy/docker/compose.yml
sudo docker compose up -d
```

Alpine users may replace `sudo` with `doas`. The data directory must be a real,
dedicated directory owned by `10001:10001`; startup fails closed instead of
running as root when it cannot safely create or protect its data.

The shipped Compose has one service, one persistent mount, and only the `TZ`
environment setting:

```text
/opt/m-ui/
├── compose.yml
└── data/                    -> /data
    ├── etc/m-ui/            -> /etc/m-ui
    ├── etc/mihomo/          -> /etc/mihomo
    ├── var/lib/m-ui/        -> /var/lib/m-ui
    └── var/lib/mihomo/      -> /var/lib/mihomo
```

The standard paths are trusted image-provided adapters, so existing m-ui
configuration and database path values remain valid. Host data may not contain
symbolic links or special files.

## First administrator and settings

The panel initially listens on every host IPv4 interface at `0.0.0.0:2095`.
Open `http://SERVER_IP:2095/`, then print the durable one-time setup link:

```sh
sudo docker compose -f /opt/m-ui/compose.yml exec m-ui m-ui admin setup-link
```

If the command prints a loopback URL, preserve its `/setup#token=...` path and
replace only the host with the server address. Create the administrator in the
Web page. Panel bind,
Public Host, Mihomo Controller endpoints and CORS, core channel, automatic
updates, and the check interval are managed from the Web system settings. Safe
first-start defaults keep the Mihomo Controller loopback-only; the panel is
reachable on all IPv4 interfaces, the core uses the release channel, automatic
updates are off, and the check interval is 24 hours. Endpoint changes which report a restart
requirement are applied with:

```sh
sudo docker compose -f /opt/m-ui/compose.yml restart m-ui
```

No administrator password, Controller secret, or other credential belongs in
Compose, an environment variable, a Docker Secret, or generated deployment
file.

## Operations

```sh
cd /opt/m-ui
sudo docker compose ps
sudo docker compose logs --tail=100 -f m-ui

# Pull the newest stable image and recreate the service.
sudo docker compose pull
sudo docker compose up -d

# Stop the service while retaining /opt/m-ui/data.
sudo docker compose down
```

To use another persistence directory, edit the single volume source in
`compose.yml`, then prepare that exact directory with UID/GID `10001:10001` and
mode `0700`. Do not use a symlink or shared system directory.

Host networking is required because Web-created Mihomo Listeners may bind
arbitrary host ports. The default panel bind also exposes the panel and complete
management API on port 2095. Protect those ports with HTTPS, a VPN, access
control, and the host firewall, or change the panel bind to `127.0.0.1` in Web
settings. m-ui does not change firewall, SSH, reverse-proxy, or Cloudflare settings.

Back up `/opt/m-ui/data` as one stopped consistency set. In particular,
`var/lib/m-ui/master.key` and `var/lib/m-ui/m-ui.db` must remain together.
`docker compose down --volumes` does not remove bind-mounted host data.

## Migrating the former bind layout

The immediately preceding Compose layout stored four trees directly below
`/opt/m-ui`. Stop it, then copy and validate those trees into the new data mount:

```sh
sudo docker compose -f /opt/m-ui/compose.yml down
sudo deploy/docker/migrate-volumes.sh \
  --source-root /opt/m-ui \
  --target /opt/m-ui/data \
  --dry-run
sudo deploy/docker/migrate-volumes.sh \
  --source-root /opt/m-ui \
  --target /opt/m-ui/data \
  --yes
```

Use the migration script from the new source tree or Release assets, not the
old copy beside the deployment.

## Migrating legacy named volumes

For the older four-volume layout, stop the project and identify its Compose
project name:

```sh
sudo docker compose -p old-m-ui -f deploy/docker/compose.legacy.yml down
sudo deploy/docker/migrate-volumes.sh \
  --source-project old-m-ui \
  --target /opt/m-ui/data \
  --dry-run
sudo deploy/docker/migrate-volumes.sh \
  --source-project old-m-ui \
  --target /opt/m-ui/data \
  --yes
```

Both migration modes require an empty target, refuse active sources and unsafe
files, copy through a staging directory, verify the database/master-key pair,
and run the image database doctor before activation. Sources are never removed
or modified. Keep them until the migrated service passes health, login,
configuration publication, and restart checks.

`compose.legacy.yml` exists only to identify and recover the legacy named-volume
shape. A rollback to an older image must use the Compose file shipped with that
older Release.

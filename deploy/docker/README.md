# Docker deployment

The image includes m-ui and a verified Mihomo bootstrap. The long-running
service uses UID/GID `10001:10001`; only the finite, network-isolated
`data-init` service runs as root to prepare ownership below the selected data
root.

## Quick start

With Docker Engine and Docker Compose v2 installed, deployment is three steps:

```sh
sudo mkdir -p /opt/m-ui && cd /opt/m-ui
sudo curl -fsSLo compose.yml https://raw.githubusercontent.com/Aethersailor/m-ui/master/deploy/docker/compose.yml
sudo docker compose up -d
```

The formal Release Compose file pins its matching image version. The file on
`master` follows the CI-verified `edge` image. Alpine users may replace `sudo`
with `doas`.

Compose and the one-shot initializer prepare this deterministic layout without
touching the Compose file; the initializer rejects links and special files
before the long-running service starts:

```text
/opt/m-ui/
├── compose.yml
├── etc/m-ui/       -> /etc/m-ui
├── etc/mihomo/     -> /etc/mihomo
├── var/lib/m-ui/   -> /var/lib/m-ui
└── var/lib/mihomo/ -> /var/lib/mihomo
```

No password file, environment variable, Docker Secret, or generated
administrator password is used.

## First administrator

The panel listens on host loopback `127.0.0.1:2095`. Establish an SSH tunnel,
then print the durable one-time setup link:

```sh
ssh -N -L 2095:127.0.0.1:2095 user@server
sudo docker compose -f /opt/m-ui/compose.yml exec m-ui m-ui admin setup-link
```

Open the printed link locally and create the administrator username and
password in the Web page. The capability stays in the URL fragment and is
consumed atomically. If it is disclosed before use, rotate it with
`m-ui admin rotate-setup-token` through the same Compose command path.

## Operations

```sh
cd /opt/m-ui
sudo docker compose ps
sudo docker compose logs --tail=100 -f m-ui

# Update the selected tag and recreate safely.
sudo docker compose pull
sudo docker compose up -d

# Stop containers while retaining all bind-mounted data.
sudo docker compose down
```

To use another persistence root, create it first and pass the same value to
every Compose command:

```sh
sudo mkdir -p /srv/m-ui
sudo env M_UI_DATA_DIR=/srv/m-ui docker compose -f /opt/m-ui/compose.yml up -d
```

Use a dedicated real directory, not a symlink or a shared system directory.
Host networking is required because Web-created Mihomo Listeners may bind
arbitrary host ports. Protect those ports with the host firewall; m-ui does
not change firewall, SSH, reverse-proxy, or Cloudflare settings.

Back up the complete persistence root as one stopped consistency set. In
particular, `var/lib/m-ui/master.key` and `var/lib/m-ui/m-ui.db` must remain
together. `docker compose down --volumes` still does not remove bind-mounted
host data; delete the selected host root only after an explicit backup and
path check.

## Migrating legacy named volumes

Stop the legacy project and perform a dry run before copying:

```sh
sudo docker compose -p old-m-ui -f deploy/docker/compose.legacy.yml down
sudo deploy/docker/migrate-volumes.sh \
  --source-project old-m-ui \
  --target /opt/m-ui \
  --dry-run
sudo deploy/docker/migrate-volumes.sh \
  --source-project old-m-ui \
  --target /opt/m-ui \
  --yes
```

Migration requires an empty target, refuses active source volumes and unsafe
files, copies through a staging directory, verifies the database/master-key
pair, and runs the image database doctor before activation. Source volumes are
never removed or modified. Keep them until the migrated deployment passes its
health and login checks.

The old shape remains in `compose.legacy.yml` only for migration and rollback.

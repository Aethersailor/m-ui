# m-ui

m-ui is a small, security-focused web control plane for one local
[Mihomo](https://github.com/MetaCubeX/mihomo) instance. It owns the
configuration it manages: SQLite is the source of truth and `config.yaml` is a
deterministic, validated build artifact.

Version 0.1 supports:

- one administrator and one local Mihomo instance;
- VLESS over TCP with REALITY and XTLS Vision;
- listener and user lifecycle management, expiry, sharing links, QR codes, and
  Mihomo client YAML;
- transactional validation, publication, health checks, revision history, and
  rollback;
- an English/Chinese responsive web interface with light and dark themes;
- Debian 12+ and Ubuntu 24.04+ on amd64 or arm64 with systemd.

## Important limits

m-ui v0.1 does **not** provide Docker deployment, other protocols or
transports, multi-node management, RBAC, public subscription endpoints,
arbitrary YAML import/editing, or precise per-user traffic accounting.
Dashboard traffic, memory, and connection values belong to the whole Mihomo
instance and must not be used for user billing.

## Installation

Review the release installer before running it:

```sh
version=v0.1.0
curl -fLO "https://github.com/Aethersailor/m-ui/releases/download/${version}/install.sh"
curl -fLO "https://github.com/Aethersailor/m-ui/releases/download/${version}/SHA256SUMS"
grep ' install.sh$' SHA256SUMS | sha256sum -c -
sudo sh install.sh --version "$version"
```

The installer:

- verifies the m-ui archive and the pinned official Mihomo v1.19.29 asset;
- creates non-login `m-ui` and `mihomo` service users;
- installs hardened systemd units and a narrowly scoped sudoers policy;
- creates a master key, Controller secret, and initial administrator password;
- validates the initial YAML with real `mihomo -t`;
- starts both services on loopback-only management addresses.

It does not alter SSH, firewall, Caddy/Nginx, or Cloudflare configuration. The
initial password is printed once. Store it immediately.

Open the panel through an SSH tunnel:

```sh
ssh -L 2095:127.0.0.1:2095 user@server
```

Then visit `http://127.0.0.1:2095/`. For HTTPS reverse-proxy examples, see
[docs/reverse-proxy.md](docs/reverse-proxy.md).

## Service operations

```sh
sudo systemctl status m-ui mihomo
sudo journalctl -u m-ui -u mihomo --since today
sudo systemctl restart m-ui
```

The web interface can start, stop, restart, or reload only
`mihomo.service`. The `m-ui` service account has no unrestricted sudo access.

Reset the administrator password without putting it in shell history:

```sh
sudo install -o m-ui -g m-ui -m 0600 /dev/null /var/lib/m-ui/new-password
sudoedit /var/lib/m-ui/new-password
sudo -u m-ui /usr/local/bin/m-ui admin reset-password \
  --config /etc/m-ui/config.toml \
  --password-file /var/lib/m-ui/new-password
sudo rm -f /var/lib/m-ui/new-password
sudo systemctl restart m-ui
```

Run deployment diagnostics and validate the current database-derived
configuration as the service account:

```sh
sudo -u m-ui /usr/local/bin/m-ui doctor --config /etc/m-ui/config.toml
sudo -u m-ui /usr/local/bin/m-ui config validate \
  --config /etc/m-ui/config.toml
```

An emergency CLI rollback uses the immutable revision ID shown by the API or
database-backed configuration history:

```sh
sudo -u m-ui /usr/local/bin/m-ui config rollback \
  --config /etc/m-ui/config.toml REVISION_ID
```

## Data and backup

Back up these paths together while both services are stopped:

```text
/etc/m-ui/
/etc/mihomo/
/var/lib/m-ui/
```

The master key and database are a pair. A database backup without the matching
`master.key` cannot decrypt managed Controller and REALITY secrets. See
[docs/configuration-lifecycle.md](docs/configuration-lifecycle.md).

## Uninstallation

Default uninstallation removes services and installed program files while
preserving configuration and data:

```sh
sudo sh scripts/uninstall.sh
```

Permanent removal requires an additional confirmation:

```sh
sudo sh scripts/uninstall.sh --purge
```

## Development

Required tools are Go 1.26.5, Node.js 24.18.0, npm, GNU Make, and Linux for the
real Mihomo smoke test.

```sh
npm --prefix web ci
go test ./...
go vet ./...
go test -race ./...
npm --prefix web run lint
npm --prefix web run typecheck
npm --prefix web run test
npm --prefix web run build
make build
make smoke
```

`make smoke` downloads only the pinned official Mihomo release, verifies its
official GitHub asset digest, compiles a synthetic managed configuration,
validates it, starts a temporary core, calls `/version`, and reloads the
configuration. It never connects to a real proxy destination.

Further reading:

- [Architecture](docs/architecture.md)
- [Security](docs/security.md)
- [Configuration lifecycle](docs/configuration-lifecycle.md)
- [Reverse proxy](docs/reverse-proxy.md)
- [Troubleshooting](docs/troubleshooting.md)

## License

GPL-3.0. See [LICENSE](LICENSE).

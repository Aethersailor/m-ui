# Security model

## Trust boundary

m-ui v0.1 is an administrator-only control plane for one local Mihomo
instance. It is not designed as a public multi-tenant service. The panel and
Mihomo Controller listen on loopback by default. Expose the panel only through
an SSH tunnel or a carefully configured HTTPS reverse proxy.

The database is the source of truth. m-ui accepts typed management operations,
not arbitrary YAML or command lines. Every configuration mutation is compiled,
validated by a fixed `mihomo -t -f <candidate>` invocation, and published by
the single transaction coordinator.

## Accounts and browser security

- Passwords use Argon2id and are never stored in plaintext.
- Session tokens and CSRF tokens are stored as hashes.
- Authenticated mutations require both the session cookie and the CSRF header.
- Login attempts are rate limited and return generic authentication failures.
- Session cookies are HttpOnly and SameSite=Strict. Set `cookie_secure = true`
  before serving the panel through HTTPS.
- Password changes and administrative resets revoke existing sessions.

The application has one administrator in v0.1. RBAC and delegated accounts are
deliberately out of scope.

## Secrets

`/var/lib/m-ui/master.key` is a 32-byte local encryption key with mode `0600`.
The Controller secret and REALITY private keys are encrypted before they are
written to SQLite.

The installer briefly uses the Controller secret to seed both systems. After a
successful first start, it removes the bootstrap value from
`/etc/m-ui/config.toml`. Mihomo still requires the same secret in its generated
`/etc/mihomo/config.yaml`; that file is restricted to `m-ui:mihomo` mode
`0640`. The setgid configuration directory preserves the `mihomo` group across
atomic publications, while revision and rollback artifacts remain `0600`.

Never copy these values into issues, chat logs, screenshots, shell history, or
monitoring labels:

- passwords and session/CSRF tokens;
- `master.key`;
- Controller secrets;
- REALITY private keys;
- full UUIDs and full sharing links.

Configuration preview is redacted by default. Revealing the live configuration
requires an explicit confirmation and the response is marked `no-store`.

## Service privileges

Both long-running services run as dedicated non-root users:

- `m-ui` owns its database, key, and revision directory and can write only the
  managed Mihomo configuration directory;
- `mihomo` reads the managed configuration and writes only its private runtime
  directory;
- Mihomo receives only `CAP_NET_BIND_SERVICE`, which permits configured ports
  below 1024 without root;
- the m-ui sudoers policy allows only start, stop, restart, reload, and
  is-active operations for the literal `mihomo.service` unit.

m-ui intentionally does not have a general shell, package-manager access,
firewall access, or permission to manage arbitrary systemd units.

## Logs and audit records

Logs go to journald by default. Application errors are redacted before logging.
Audit rows contain an action, resource identifier, result, and a short redacted
summary. They must never contain request bodies, credentials, complete UUIDs,
private keys, Controller secrets, or sharing links.

## Network exposure

The installer never opens firewall ports or changes SSH, reverse-proxy, or
Cloudflare configuration. Listener ports are a deliberate administrator
choice. The panel remains at `127.0.0.1:2095` and the Controller remains at
`127.0.0.1:9090`.

When using a reverse proxy:

- terminate TLS at the proxy;
- keep the backend on loopback;
- set `cookie_secure = true`;
- do not cache `/api/`;
- apply independent authentication or IP restrictions if appropriate;
- preserve request size and timeout limits.

See [reverse-proxy.md](reverse-proxy.md) for minimal examples.

## Backups and incident response

Back up `/etc/m-ui`, `/etc/mihomo`, and `/var/lib/m-ui` as one consistency set.
Protect backups at least as strongly as the live host. If `master.key` is
exposed, rotate all Controller and REALITY credentials and user UUIDs after
restoring on a trusted machine.

If m-ui reports degraded mode, stop making configuration changes, preserve
journald output and the revision directory, and follow
[troubleshooting.md](troubleshooting.md). Do not delete the active YAML or
database to clear the indicator.

Security reports should be sent privately to the repository maintainer. Do not
open a public issue containing exploit details or secrets.

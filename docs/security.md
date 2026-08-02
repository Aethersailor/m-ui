# Security model

## Trust boundary

m-ui v0.1 is an administrator-only control plane for one local Mihomo
instance. It is not designed as a public multi-tenant service. The panel UI
and Mihomo `external-controller` dashboard API listen on loopback by default.
They are separate listeners: m-ui's `/api/v1` is not the Mihomo dashboard API.
Expose either one only through an SSH tunnel, VPN, or carefully configured
HTTPS reverse proxy.

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
- the Alpine doas policy allows only the equivalent literal
  `/sbin/rc-service mihomo` operations;
- the OCI image runs as `10001:10001`, drops all capabilities except
  `NET_BIND_SERVICE`, and uses no init-system or package-management privilege.

m-ui intentionally does not have a general shell, package-manager access,
firewall access, or permission to manage arbitrary systemd units.

Package upgrade hooks keep service-state handoff data in a separate
`root:root` `0700` directory under `/run`, never in the m-ui runtime directory,
and parse only fixed `0`/`1` fields. Each package transaction records its exact
root-owned snapshot path in the same protected directory; postinstall never
guesses a snapshot by timestamp or filename ordering. Package rollback
snapshots are kept under the root-only `/var/lib/m-ui-package-backups` tree;
snapshots containing links, sockets, or other special files are rejected before
privileged restoration, and restored service state must pass the active panel
HTTP health endpoint before the handoff is consumed.

The managed core tree is owned by `m-ui:mihomo`. Parent directories give
Mihomo traversal/read/execute access to only the current verified binary while
database, master key and revision data remain private to m-ui. The updater
rejects symbolic links, group/other-writable files, unexpected owners, invalid
manifests, oversized content and digest mismatches.

## Build and update supply chain

Runtime core resolution is fixed to the official `MetaCubeX/mihomo` repository
and trusted GitHub HTTPS hosts. Redirect count, response sizes, asset sizes and
decompressed sizes are bounded. Optional API tokens come only from
`M_UI_GITHUB_TOKEN`, are never placed in URLs or logs, and are not copied into
download requests.

Release builds pin every GitHub Action by commit SHA. The build locks one
Mihomo release identity, verifies its API digest, and executes each architecture
on a native runner. tar, deb, apk and OCI artifacts use the same identity and
m-ui commit. SPDX SBOMs and SHA-256 checksums accompany downloadable artifacts.
The formal release workflow is manual, requires an exact semantic version and
the exact current `origin/master` SHA, and can complete a dry run without
creating any remote object.

## Logs and audit records

Logs go to journald by default. Application errors are redacted before logging.
Audit rows contain an action, resource identifier, result, and a short redacted
summary. They must never contain request bodies, credentials, complete UUIDs,
private keys, Controller secrets, or sharing links.

## Network exposure

The installer never opens firewall ports or changes SSH, reverse-proxy, or
Cloudflare configuration. Listener ports are a deliberate administrator
choice. The panel UI remains at `127.0.0.1:2095` and the Mihomo
`external-controller` remains at `127.0.0.1:9090` until the administrator
changes them in System settings. The m-ui-to-Mihomo connection target remains
loopback-only and is never allowed to be an arbitrary remote host.

Endpoint changes are stored in SQLite and the generated YAML is validated, but
they are not treated as live socket changes. The UI reports whether m-ui or
Mihomo must be restarted. CORS accepts exact `http://`/`https://` origins only;
`*` is rejected. Public dashboard access should use TLS plus a VPN, allowlist,
or an independently managed reverse proxy, and must still supply the Mihomo
Controller secret.

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

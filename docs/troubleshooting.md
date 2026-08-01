# Troubleshooting

## Collect a safe status snapshot

```sh
sudo systemctl --no-pager --full status m-ui mihomo
sudo journalctl -u m-ui -u mihomo -n 200 --no-pager
/usr/bin/m-ui version
/var/lib/m-ui/core/current/mihomo -v
sudo -u m-ui /usr/bin/m-ui doctor --config /etc/m-ui/config.toml
sudo -u m-ui /usr/bin/m-ui core status \
  --json --config /etc/m-ui/config.toml
sudo -u m-ui test -r /etc/m-ui/config.toml
sudo -u m-ui test -w /var/lib/m-ui
sudo -u mihomo test -r /etc/mihomo/config.yaml
```

Redact full UUIDs, sharing links, Controller secrets, REALITY private keys,
session tokens, and passwords before sharing output.

## m-ui does not start

Check the journal first:

```sh
sudo journalctl -u m-ui -b --no-pager
```

Common causes are a missing or overly broad `master.key`, an unwritable SQLite
directory, invalid TOML, or a port conflict on `127.0.0.1:2095`.

Required modes:

```text
/etc/m-ui                  root:m-ui 0750
/etc/m-ui/config.toml      root:m-ui 0640
/etc/mihomo                m-ui:mihomo 2750
/etc/mihomo/config.yaml    m-ui:mihomo 0640
/var/lib/m-ui              m-ui:mihomo 0710
/var/lib/m-ui/core         m-ui:mihomo 2710
/var/lib/m-ui/core/current m-ui:mihomo 0750
/var/lib/m-ui/master.key   m-ui:m-ui 0600
```

Do not generate a new master key for an existing database. The existing
encrypted values would become unreadable.

## Mihomo does not start

Validate the active configuration without changing it:

```sh
sudo -u mihomo /var/lib/m-ui/core/current/mihomo \
  -t -d /var/lib/mihomo -f /etc/mihomo/config.yaml
sudo journalctl -u mihomo -b --no-pager
```

A port conflict, invalid listener field, or missing read permission is usually
reported here. m-ui validates candidates before publication, so an invalid
candidate should not replace the previously working configuration.

## Runtime actions fail

Check the exact sudoers file and command:

```sh
sudo visudo -cf /etc/sudoers.d/m-ui
sudo -u m-ui sudo -n /usr/bin/systemctl is-active mihomo.service
```

The policy intentionally rejects other services, arguments, and commands.
Restore `/etc/sudoers.d/m-ui` from the release package instead of broadening it.

## Dashboard says offline

Confirm both the service and authenticated Controller:

```sh
sudo systemctl is-active mihomo.service
sudo ss -lntp | grep '127.0.0.1:9090'
```

The Controller secret must match the managed Mihomo YAML. Do not post either
file publicly. If the service is active but the panel remains offline, inspect
the m-ui journal for a redacted Controller error.

## Core check or update fails

Start with read-only status:

```sh
sudo -u m-ui /usr/bin/m-ui core status \
  --json --config /etc/m-ui/config.toml
sudo -u m-ui /usr/bin/m-ui core check \
  --config /etc/m-ui/config.toml
```

A busy response means a configuration publication, runtime action, or another
core operation owns the shared coordinator; wait for that bounded operation to
finish. An external-core response means updates are intentionally disabled for
the preserved custom binary path. Rate-limit and network failures leave the
current binary unchanged.

If activation validation fails, inspect the redacted m-ui/Mihomo logs and the
manifest under `core/current`; do not copy a new binary into place. A successful
automatic rollback records a failed update but does not enter degraded mode.
If both activation and rollback fail, follow the degraded procedure and
preserve `core/current`, `core/backups`, SQLite and logs together.

## Degraded mode

Degraded mode means m-ui could not prove or restore a consistent relationship
between the database, unique active revision, revision YAML/JSON artifacts, and
active Mihomo YAML. It can be entered after an uncertain `COMMIT` result, a
failed automatic repair, or startup integrity checking. Do not keep retrying
mutations.

1. Stop m-ui to freeze changes.
2. Back up `/etc/m-ui`, `/etc/mihomo`, and `/var/lib/m-ui`.
3. Inspect both service journals, the indicated active revision metadata, and
   its `.yaml` and `.json` files under the revision directory.
4. Compare the active YAML SHA-256 with the active revision and validate the
   YAML with real Mihomo.
5. Restore a known-good matched backup of configuration, database, revision
   artifacts, and master key, or investigate with the maintainer.

The HTTP panel and read-only runtime monitor remain available in degraded mode,
but configuration writes return the degraded error and the expiry scheduler is
not started. A first installation with no active revision is not degraded and
startup intentionally leaves its bootstrap YAML untouched.

Deleting `system_state`, revisions, or the database is not a repair.

If startup exits instead of serving a degraded read-only panel, inspect for a
second database/storage failure while persisting the degraded marker. This
fail-closed outcome is intentional: m-ui will not continue when it cannot prove
that the durable safety state matches memory.

## Revision cleanup warning

If the journal reports that publication succeeded but revision maintenance
failed, the configuration and database commit remain successful. Do not repeat
the mutation. Check revision-directory ownership, free space, file types, and
the database error, then repair the maintenance cause. Both `failed` and
`rolled_back` revisions count toward the inactive history limit; the active
revision is never eligible for cleanup.

## Login rate limiting

Repeated failures produce HTTP 429 with `Retry-After`. Wait for that interval
instead of restarting the service. For a lost password, use the password-file
reset procedure in the README; it revokes existing sessions.

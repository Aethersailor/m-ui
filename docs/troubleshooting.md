# Troubleshooting

## Collect a safe status snapshot

```sh
sudo systemctl --no-pager --full status m-ui mihomo
sudo journalctl -u m-ui -u mihomo -n 200 --no-pager
/usr/local/bin/m-ui version
/usr/local/bin/mihomo -v
sudo -u m-ui /usr/local/bin/m-ui doctor --config /etc/m-ui/config.toml
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
/var/lib/m-ui              m-ui:m-ui 0700
/var/lib/m-ui/master.key   m-ui:m-ui 0600
```

Do not generate a new master key for an existing database. The existing
encrypted values would become unreadable.

## Mihomo does not start

Validate the active configuration without changing it:

```sh
sudo -u mihomo /usr/local/bin/mihomo \
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

## Degraded mode

Degraded mode means automatic recovery after a publication failure did not
fully restore file and database state. Do not keep retrying mutations.

1. Stop m-ui to freeze changes.
2. Back up `/etc/m-ui`, `/etc/mihomo`, and `/var/lib/m-ui`.
3. Inspect both service journals and revision files.
4. Validate the active YAML with real Mihomo.
5. Restore a known-good matched backup of configuration, database, and master
   key, or investigate with the maintainer.

Deleting `system_state`, revisions, or the database is not a repair.

## Login rate limiting

Repeated failures produce HTTP 429 with `Retry-After`. Wait for that interval
instead of restarting the service. For a lost password, use the password-file
reset procedure in the README; it revokes existing sessions.

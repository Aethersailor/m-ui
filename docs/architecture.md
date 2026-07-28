# m-ui v0.1 architecture

## Purpose and boundary

m-ui is a single-host administration service for one dedicated Mihomo server
instance. It manages multiple VLESS TCP listeners using REALITY and XTLS Vision,
with multiple users per listener. It is not a Mihomo dashboard, a general YAML
editor, a subscription aggregator, or a multi-node controller.

The SQLite database is the authoritative representation of administrator,
listener, user, settings, revision, and audit state. Mihomo YAML is a
deterministic compiled artifact owned exclusively by m-ui.

## Runtime topology

```text
administrator browser
        |
        | HTTP on 127.0.0.1:2095 by default
        v
m-ui single binary (user m-ui)
  |-- embedded Vue application
  |-- REST API and authentication
  |-- SQLite store
  |-- desired-state compiler
  |-- transactional publisher
  |-- runtime collector
  |-- expiry scheduler
  |
  |-- fixed CLI invocation ------------> mihomo binary
  |-- loopback REST API ---------------> Mihomo controller
  `-- fixed sudo systemctl commands ---> mihomo.service (user mihomo)
```

m-ui and Mihomo run as separate systemd services and separate unprivileged
users. m-ui receives no general root or shell access. Its sudoers entry permits
only fixed lifecycle operations on `mihomo.service`.

## Package responsibilities

- `cmd/m-ui`: CLI entry point and command routing.
- `internal/app`: dependency assembly, process lifecycle, and server startup.
- `internal/auth`: password hashing, sessions, CSRF, and login throttling.
- `internal/audit`: redacted security and configuration audit events.
- `internal/config`: protected local m-ui configuration loading and validation.
- `internal/crypto`: master-key loading and versioned AES-256-GCM envelopes.
- `internal/domain`: typed listener, user, settings, revision, and desired-state
  models plus validation.
- `internal/httpapi`: REST routing, middleware, DTOs, and stable error responses.
- `internal/mihomo`: Controller HTTP client, fixed-argument CLI runner, systemd
  adapter, and runtime snapshots.
- `internal/publisher`: compilation, candidate validation, atomic publication,
  health checking, revision management, and recovery.
- `internal/scheduler`: UTC expiry scans and batched publication.
- `internal/service`: use-case orchestration independent from HTTP transport.
- `internal/store`: SQLite migrations and repositories using `database/sql`.
- `internal/version`: build-injected version, commit, date, and dirty state.

The Vue application under `web/` is built to static assets and embedded into the
Go binary. It communicates only with `/api/v1` on the same origin and loads no
runtime assets from external CDNs.

## Data and request flow

Reads query the service layer, which obtains structured state from the store or
an in-memory runtime snapshot. Secret values are decrypted only at the boundary
that needs them and are never returned through normal list or audit endpoints.

Every listener or user mutation follows one publication path:

```text
HTTP or scheduler request
  -> authenticate, authorize and validate
  -> acquire global publish lock
  -> BEGIN IMMEDIATE
  -> mutate structured state in the transaction
  -> compile deterministic candidate
  -> write and fsync same-filesystem candidate
  -> run `mihomo -t -f <candidate>`
  -> save YAML plus versioned DesiredState JSON revision
  -> atomic rename and directory fsync
  -> Controller reload, with fixed systemd recovery when required
  -> bounded health check
  -> write revision and audit metadata
  -> commit database transaction
```

Any failure before commit restores the previous file and runtime state and
rolls back database changes. A database commit failure also restores and
reloads the old configuration. If recovery itself fails, m-ui records degraded
state and rejects subsequent mutations until an operator repairs the indicated
revision.

## Determinism and revisions

Compiler input is a typed `DesiredState`. Listeners and users are sorted by name
and then ID. Dedicated YAML output structures determine field order. The same
state, including the controller secret, must produce byte-identical YAML and
the same SHA-256 digest.

Successful revisions retain:

- the generated Mihomo YAML;
- a versioned JSON snapshot of the structured desired state;
- redacted metadata in SQLite.

Rollback validates the stored state, recompiles it, validates the new candidate
with Mihomo, atomically publishes it, performs the same health checks, and
updates both structured and runtime state. It never blindly copies a historical
YAML file over the active configuration.

## Security boundaries

- Administrator passwords use Argon2id with per-password random salts.
- Browser session and CSRF tokens are generated with a CSPRNG. Only SHA-256
  hashes are stored in SQLite.
- Controller secrets and REALITY private keys use a 32-byte local master key
  and versioned AES-256-GCM envelopes with a fresh nonce for every value.
- State-changing browser requests require a valid session and CSRF header.
- Login failures are deliberately indistinguishable and rate limited by the
  source-address and username combination.
- Forwarded client-address headers are ignored in v0.1; security decisions use
  the direct socket peer and therefore cannot be spoofed through HTTP headers.
- Sensitive responses use `Cache-Control: no-store`; security headers deny
  framing and constrain content sources.
- Logs, audit summaries, errors, fixtures, and command output are redacted and
  size-bounded.

## Runtime observations

The collector polls the loopback Mihomo Controller for version, traffic, memory,
and connections. It stores only recent in-memory snapshots. Collection failure
marks runtime metrics offline but does not block safe configuration management.
All displayed traffic and connection values are labeled as instance-level
observability, never user billing data.

## Deployment invariants

- Supported targets are systemd-based Debian 12+ and Ubuntu 24.04+ on Linux
  amd64 and arm64.
- The panel defaults to `127.0.0.1:2095`; remote access uses an SSH tunnel or a
  separately managed loopback reverse proxy.
- The Mihomo Controller remains on loopback.
- Data, the master key, database, revisions, and generated configuration use
  least-privilege ownership and modes.
- Installation does not modify the firewall, SSH configuration, reverse proxy,
  or third-party infrastructure.

## v0.1 non-goals

No other protocols or transports, multi-node control, multi-administrator RBAC,
per-user quota enforcement, permanent public subscriptions, arbitrary
third-party YAML import, Docker packaging, automatic TLS certificates, firewall
automation, or reverse-proxy installation are part of v0.1.

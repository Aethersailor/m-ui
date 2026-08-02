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
  |-- verified core updater and scheduler
  |-- shared runtime operation coordinator
  |-- runtime collector
  |-- expiry scheduler
  |
  |-- fixed CLI invocation ------------> mihomo binary
  |-- m-ui REST API and panel UI ------> administrator browser
  |-- loopback client connection ------> Mihomo controller
  `-- fixed service adapter ----------> systemd/OpenRC/managed Mihomo
```

On native systems m-ui and Mihomo run as separate services and separate
unprivileged users. m-ui receives no general root or shell access. Debian and
Ubuntu use a fixed sudoers policy for `mihomo.service`; Alpine uses a fixed doas
policy for the literal `rc-service mihomo` operations. In the OCI image one
non-root UID runs m-ui and its bounded Mihomo supervisor without an init system.

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
  and OpenRC adapters, non-root managed supervisor, and runtime snapshots.
- `internal/core`: fixed-upstream release resolution, digest verification,
  candidate staging, activation, rollback, and core manifests.
- `internal/operation`: shared exclusion for publication, core lifecycle, and
  manual runtime operations.
- `internal/publisher`: compilation, candidate validation, atomic publication,
  health checking, revision management, and recovery.
- `internal/scheduler`: UTC expiry scans and batched publication.
  It also schedules optional core checks/updates with bounded backoff.
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

Endpoint settings use the same typed publication transaction, but mark the
affected runtime boundary as pending instead of assuming a reload changed a
listening socket. The candidate YAML and active SQLite state are committed
only after Mihomo validation; the pending snapshot is cleared separately when
m-ui or Mihomo has successfully restarted. A panel bind change is applied on
the next m-ui restart, while an `external-controller` bind/CORS change is
applied on the next Mihomo restart.

Core updates use the same operation coordinator as configuration publication
and runtime actions. A candidate is selected by exact Linux architecture from a
fixed official GitHub repository, size- and digest-checked, decompressed into a
same-filesystem staging directory, executed for version and configuration
validation, then atomically activated. The previous core remains a bounded
backup until the new process and authenticated Controller are healthy and
durable state is committed. A failed activation restores and restarts the old
core using an independent bounded recovery context; inability to restore is a
degraded condition.

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

- Supported native targets are Debian 12+/sid and Ubuntu 24.04+ with systemd
  and Alpine 3.20+ with OpenRC, on Linux amd64 and arm64.
- OCI images are non-root Linux amd64/arm64 images with a direct Mihomo
  supervisor and persistent configuration/data volumes.
- The panel UI defaults to `127.0.0.1:2095`. Its bind host/port is a separate
  managed endpoint and may be changed to an IPv4/IPv6 bind address from the
  System settings page; the m-ui service must be restarted before it is active.
- Mihomo's `external-controller` dashboard API is a different endpoint from
  m-ui's `/api/v1`. It defaults to `127.0.0.1:9090`; its bind host/port and
  exact CORS origins are managed separately, published into YAML, and require
  an explicit Mihomo restart. m-ui's own Controller client remains a distinct
  loopback-only connection target.
- Data, the master key, database, revisions, and generated configuration use
  least-privilege ownership and modes.
- Installation does not modify the firewall, SSH configuration, reverse proxy,
  or third-party infrastructure.

## v0.1 non-goals

No other protocols or transports, multi-node control, multi-administrator RBAC,
per-user quota enforcement, permanent public subscriptions, arbitrary
third-party YAML import, automatic TLS certificates, firewall automation, or
reverse-proxy installation are part of v0.1.

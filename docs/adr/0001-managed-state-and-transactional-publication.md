# ADR 0001: Managed state and transactional publication

- Status: Accepted
- Date: 2026-07-28
- Scope: m-ui v0.1

## Context

m-ui must provide form-based listener and user management without corrupting a
running Mihomo instance. Editing arbitrary YAML in place would make validation,
redaction, deterministic output, rollback, and database consistency unreliable.
Separately updating SQLite and `/etc/mihomo/config.yaml` would create failure
windows where the UI and live service describe different states.

The v0.1 deployment is intentionally one m-ui process managing one dedicated
Mihomo process. This permits a single serialization boundary and avoids a
distributed consensus problem.

## Decision

SQLite structured objects are the source of truth. m-ui exclusively owns the
Mihomo file it generates, and a typed compiler turns `DesiredState` into
deterministic YAML.

All listener, user, setting, expiry, and rollback mutations pass through one
transactional publisher. The publisher serializes changes, starts a SQLite
`BEGIN IMMEDIATE` transaction, applies the proposed structured mutation,
compiles and fsyncs a same-filesystem candidate, validates it with a
fixed-argument Mihomo CLI invocation, saves a YAML and JSON revision, performs
an atomic rename, reloads Mihomo, checks health, then commits SQLite.

Any failure restores the prior generated file and runtime configuration and
rolls back SQLite. A failed recovery places m-ui in degraded mode and rejects
further mutations. Historical rollback restores the versioned structured JSON
snapshot and recompiles it; it is not a raw file-copy operation.

## Consequences

Benefits:

- Identical state yields identical YAML bytes and hashes.
- Invalid candidates never replace the active configuration.
- Runtime state, database state, and revision history have one consistency
  boundary.
- Core, Controller, and process interfaces can be replaced with fakes for
  failure-path integration tests.
- YAML output can evolve behind typed adapters without exposing general YAML
  editing.

Costs:

- Mutations are serialized and may wait on CLI validation and health checks.
- SQLite transactions remain open across filesystem and local-process work.
- Publisher recovery and commit-failure handling require careful testing.
- m-ui cannot preserve comments or unknown fields from third-party YAML.

These costs are accepted for a single-host administration plane where safety
and recoverability are more important than mutation throughput.

## Rejected alternatives

### Read, modify, and rewrite arbitrary Mihomo YAML

Rejected because comments, aliases, ordering, unknown fields, and concurrent
external edits cannot be preserved safely while still guaranteeing redaction
and deterministic output.

### Treat generated YAML as the authoritative state

Rejected because parsing generated files back into evolving business objects
weakens migrations, field encryption, auditability, and validation.

### Commit SQLite before publishing the file

Rejected because publication or reload failure would expose desired database
state that is not live.

### Publish the file before opening a database transaction

Rejected because concurrent mutations could compile stale data and database
failure could leave live state untracked.

### Split m-ui into separate API, worker, and publisher services

Rejected for v0.1 because it adds coordination, deployment, and recovery
complexity without a single-host throughput need.


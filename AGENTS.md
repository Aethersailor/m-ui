# m-ui engineering rules

## Scope

- Implement the phases in `m-ui-v0.1-roadmap-and-implementation.md` in order.
- m-ui owns the Mihomo configuration it manages. The database is the source of
  truth and YAML is a deterministic build artifact.
- v0.1 is limited to one local Mihomo instance, VLESS over TCP with REALITY and
  XTLS Vision, one administrator, and native systemd plus the already-supported
  Docker deployment on supported Linux hosts.
- Do not add other protocols or transports, multi-node
  management, RBAC, public subscription endpoints, precise per-user accounting,
  or arbitrary YAML import/editing in v0.1.

## Required verification

Run the commands that apply to the current phase and fix failures before
continuing:

```text
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

When compiler or publisher behavior changes, also run the pinned real Mihomo
validation used by the smoke test. Use only official English Mihomo
documentation, official source, and official releases to establish Mihomo
behavior.

## Configuration safety

- Never build business data with `map[string]any`; use typed domain and YAML
  adapter structures.
- All configuration-changing operations use the single publisher transaction.
- Validate candidates with fixed `exec.CommandContext` arguments before atomic
  publication. Never use a shell to execute API-derived values.
- Keep candidates on the same filesystem as the active configuration and fsync
  file and directory boundaries.
- Restore both the active file and structured state after publication failure.
  Enter degraded mode if automatic recovery fails.
- Do not log or audit passwords, controller secrets, REALITY private keys, full
  UUIDs, full sharing links, session tokens, CSRF tokens, or request bodies.
- Test data must use generated or obviously synthetic credentials only.
- First administrator setup must use a durable, one-time bootstrap capability;
  absence of an administrator, loopback source addresses, or Origin headers
  are not authorization by themselves.
- Never place an administrator password in deployment environment variables,
  process arguments, deployment files, logs, Docker Secrets, or generated
  installation artifacts.
- Bootstrap completion must atomically create the administrator, first session,
  success audit, and consumed capability. CLI password reset is recovery only
  and must not create the first administrator.

## Security and permissions

- The long-running m-ui and Mihomo services must not run as root.
- The panel listens on `127.0.0.1:2095` by default.
- Controller access is loopback-only and authenticated.
- Browser mutations require an authenticated session and CSRF header.
- Sudo access is restricted to the exact Mihomo systemd commands documented in
  `deploy/sudoers/m-ui`.
- Installation must not alter firewall, SSH, reverse proxy, or Cloudflare
  configuration.
- Docker may simplify the existing deployment only by preserving the standard
  internal paths and non-root runtime boundary. Existing named-volume data
  must have an explicit, non-destructive migration path.

## Repository and commits

- Preserve the GPL-3.0 license.
- Keep changes confined to the phase currently being implemented.
- Use Conventional Commits. Each completed phase should have one local commit.
- Local commits are allowed. Never push, create a pull request or release,
  change remote branches, secrets, or repository settings without explicit
  authorization in the current conversation.
- Do not commit generated build output, downloaded Mihomo binaries, real
  credentials, local databases, logs, coverage files, or editor caches.

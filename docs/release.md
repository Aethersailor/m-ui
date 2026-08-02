# Build and release policy

## Snapshot Build

Every pull request, branch push, and manual dispatch runs `Snapshot Build`.
The workflow:

1. records the exact m-ui commit and locks one official stable Mihomo release
   identity;
2. downloads and verifies the exact amd64-compatible and arm64 assets on native
   architecture runners;
3. executes each bootstrap with `-v` and validates the pinned golden
   configuration with `-t -f`;
4. builds m-ui Linux amd64/arm64 archives, deb and apk packages;
5. emits SHA-256 checksums and SPDX JSON SBOMs;
6. installs/removes/reinstalls the native packages in supported distro
   containers while checking data persistence;
7. builds, starts, health-checks, restarts and inspects non-root native OCI
   images on both architectures.

Snapshot artifacts are retained for 14 days. Feature-branch runs never create a
Tag, GitHub Release, or GHCR tag. A push to `master` may update only the
documented `edge` and commit-addressed snapshot image tags.

## Formal Release

Formal publishing is never triggered by a tag push. An operator must manually
dispatch `Release` with `version_mode` (`explicit`, `patch`, `minor`, or
`major`), an optional `version`, `target_ref` (default `master`), a
`prerelease` boolean, and the exact confirmation string `RELEASE`.

`explicit` accepts `vX.Y.Z` or `X.Y.Z`. The other modes inspect the latest
strict `vX.Y.Z` tag and perform correct patch/minor/major carry; if no tag
exists, patch starts at `v0.1.0`. The workflow fetches the selected remote ref,
records its exact commit, refuses an existing tag or Release, and requires
`master` to equal the fetched `origin/master` SHA. A prerelease never updates
`latest`.

Publishing is the final job and is reachable only after all native builds,
tests, package lifecycle checks and container smoke tests succeed. It validates
all assets and image digests, pushes immutable candidate architecture tags,
creates an annotated tag and draft GitHub Release, then publishes the formal
GHCR multi-architecture labels, provenance attestations and the verified draft.

Cleanup uses the GitHub Packages versions API rather than an unsupported
`docker buildx imagetools rm` command. Before deleting a package version, the
workflow enumerates every tag attached to that version. It deletes a version
only when all of its tags are run-scoped candidate or newly-created failed-
promotion tags; if a formal tag shares the version, deletion is refused and the
job summary records the retained candidate tag. The cleanup status and every
residual tag are therefore explicit and auditable. If the package API does not
grant the workflow admin access, no deletion is attempted and the summary marks
cleanup as blocked. The release contains:

```text
m-ui_<version>_linux_amd64.tar.gz
m-ui_<version>_linux_arm64.tar.gz
m-ui_<version>_linux_amd64.deb
m-ui_<version>_linux_arm64.deb
m-ui_<version>_linux_amd64.apk
m-ui_<version>_linux_arm64.apk
*.sbom.spdx.json
SHA256SUMS
manage.sh
install.sh
uninstall.sh
MIHOMO_BOOTSTRAP_IDENTITY.json
IMAGE_MANIFEST.json
```

Do not manually replace one architecture or rebuild an asset outside the
workflow. If a release candidate changes, use a new commit and rerun the entire
graph so packages, SBOMs, checksums, images and provenance identify the same
source. This task does not dispatch the formal workflow or create a Tag,
Release, or formal GHCR version label.

## Local validation

Before pushing a release candidate:

```sh
go test ./...
go vet ./...
go test -race ./...
npm --prefix web ci
npm --prefix web run lint
npm --prefix web run typecheck
npm --prefix web run test
npm --prefix web run build
make build
make smoke
actionlint
goreleaser check
```

Race, package lifecycle, native ARM, real Mihomo and Docker evidence must come
from Linux-capable local execution or the exact final-SHA GitHub Actions jobs;
a cross-compile alone is not runtime evidence.

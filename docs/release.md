# Build and release policy

m-ui has one GitHub Actions workflow:
`.github/workflows/build-release.yml` (`Smart Build & Release`). The same
workflow owns source verification, native packages, Docker images, workflow
artifacts, GHCR publication, and GitHub Releases.

## Modes

Pull requests and pushes to `master` automatically run `test`. A manual
dispatch selects one mode:

- `test`: run backend and frontend tests, vet, race detection, linters, policy
  checks, the pinned real Mihomo validation, install-script tests, embedded Web
  build, Linux cross-compilation, and dynamic-version assertions.
- `build`: run everything in `test`, then build and test amd64/arm64 tar,
  deb, apk, SBOM, checksum, Mihomo bootstrap, and Docker-image artifacts.
  Artifacts are uploaded to the workflow run; no registry tag, Git tag, or
  GitHub Release is created.
- `release`: run the complete `build` graph, attest the exact artifacts,
  publish GHCR, create the annotated source tag and draft Release, and expose
  the Release only after every identity check succeeds.

The final `complete` job checks that required jobs succeeded and that jobs
which must not run in the selected mode were skipped.

## Dynamic version identity

Every run resolves one strict base tag `vX.Y.Z` and the exact target commit.
The complete product version is:

```text
vX.Y.Z+g<12-character-commit-id>
```

For non-release runs, `auto` uses the latest strict semantic-version tag. For
release runs, `auto` increments the patch version; explicit, patch, minor, and
major modes are also available. A release refuses an existing Git tag, GitHub
Release, or GHCR version tag.

The complete version is injected into the Go binary, package metadata, Web UI
(via the health API), health JSON, and OCI image version label. The full
40-character commit is injected separately into the binary and OCI revision
label. The build date is the source commit timestamp, so rerunning the same
source identity does not invent different version metadata. Release filenames
retain the base semantic version so `install.sh` and `manage.sh` can resolve
assets deterministically from the GitHub Release tag.

## Artifact matrix

`build` and `release` produce:

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
compose.yml
MIHOMO_BOOTSTRAP_IDENTITY.json
m-ui-image-amd64.tar.gz
m-ui-image-arm64.tar.gz
m-ui-image-amd64.sbom.spdx.json
m-ui-image-arm64.sbom.spdx.json
```

A Release additionally includes `IMAGE_MANIFEST.json` and provenance
attestations. Both native architectures execute the locked Mihomo bootstrap and
validate the golden configuration. Native packages are installed, removed, and
reinstalled in supported distribution containers. Each architecture image is
built on a native runner, run as a non-root service, health checked, restarted,
version checked, and exported. The final multi-architecture publication reuses
those BuildKit caches.

## Registry policy

GHCR retains only these public image tags:

- the exact release tag, for example `v0.2.0`;
- `latest` for the newest stable release.

A prerelease publishes only its exact version tag. Push and `build` runs never
publish `edge`, commit, architecture, major, minor, candidate, or other
registry tags. The source Compose file defaults to `latest`; the Compose file
inside a GitHub Release is pinned to that Release's exact version tag.

The multi-architecture image is pushed directly with its final tags, so no
temporary registry tags are required. Before publication, the workflow
enumerates GHCR package versions through the paginated GitHub Packages API and
snapshots every tag it will change. On failure it restores prior digests and
deletes only newly-created package versions whose complete tag set is proven
safe. Ambiguous API data fails closed.

Docker Hub is deliberately disabled today. The workflow reserves
`vars.DOCKERHUB_IMAGE` as the future registry target at the same Buildx tag
boundary; adding credentials and parity rollback does not require another
compile workflow.

## Dispatching

A formal release is manual and requires `mode=release`, a version strategy,
the exact target ref, optional prerelease selection, and the confirmation value
`RELEASE`. Selecting `release` is the only path with write permissions in
the job that mutates tags, packages, attestations, or Releases.

Do not replace one architecture or rebuild a single asset outside the workflow.
If source changes, rerun the whole graph with the new commit so packages,
checksums, SBOMs, images, attestations, and the Release all identify one source.

## Local validation

Run the applicable checks before pushing:

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

Race, package lifecycle, native ARM, real Mihomo, and Docker runtime evidence
must come from Linux-capable execution or the exact final-SHA Actions run.

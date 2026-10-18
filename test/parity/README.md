---
relationships:
  references: ../../reference/cli/package.json
---

# Parity harness

Run `git submodule update --init --depth 1`, then `task parity` from the repository
root. Docker with buildx (for upstream), Go, Node and npx are needed. The upstream executable is
resolved with `npx --yes @devcontainers/cli@<version>` using the checked-out
`reference/cli/package.json`, independent of any global CLI installation.
Missing Node/npx, Docker or the CLI submodule produces a clear skip report.
The live harness is separate from `task check` and `go test ./...`.

Reports are written to `.tmp/parity/score.json`, `.tmp/parity/report.md` and
`.tmp/parity/run.log`. Override with `task parity -- --output <directory>`.
The JSON includes both normalized observations for each fixture and dimension.
The score is matched dimensions divided by all dimensions (0 to 1). Failed CLI
calls and unavailable observations count as failures, including when both sides
fail. Parity differences do not make the command fail; harness setup, report and
cleanup errors do. A skipped run has zero dimensions and a `skipped` reason;
it is not a baseline score.

The five curated upstream fixtures cover an image, a Dockerfile, two local v2
features, a Compose service from an image
(`compose-image-without-features-minimal`), and an image with a published
feature and `remoteUser` (`image-with-git-feature`). Each run copies them into
separate workspaces under `.tmp/` and adds the run ownership label
`devcontainer.parity.run` and fixture/side label
`devcontainer.parity.fixture-side` to runtime arguments. Compose ignores
`runArgs`, so for a Compose configuration the harness instead appends
`parity-labels.yml` to `dockerComposeFile`; it sets both labels on the primary
service. Discovery requires both
labels and exactly one full container ID, then verifies that ID and both labels
in inspection. It does not filter on compared labels such as
`devcontainer.local_folder`; missing or different values remain observable.
Both harness labels are excluded from scoring. Upstream Dockerfile build
options carry the run ownership label. The Go side gets no Docker CLI build
options; its derived images are owned by name (see teardown).
The fixture bodies and feature scripts otherwise stay intact. No credentials or
host Docker configuration are copied. Every subprocess receives a private
`DOCKER_CONFIG` containing only `{}` and no inherited `DOCKER_AUTH_CONFIG`.
Daemon connection variables remain available.

Dimensions compare the CLI JSON result, image `devcontainer.metadata`, container
environment, labels, mounts, user, working directory, lifecycle marker contents,
and generated feature executables (contents, executable permission and output).
Lifecycle probes read `/postCreateCommand.txt`; feature probes run `hello` and
`color`. Fixtures with no applicable probes produce empty maps. Observations
still inspect an existing container after a lifecycle failure.

Normalization replaces observed container/image IDs, hostnames, temporary
workspace paths and the workspace folder name. Result timestamps are ignored.
Environment and label maps and mount sets are unordered; metadata and command
arrays retain their order.
Unexpected environment values, metadata differences and missing results remain
visible. The dcb adapter reads the real JSON success result; it rejects extra
stdout lines, invalid JSON, and error outcomes. It does not manufacture a result
from inspection.

`Adapter` in `adapter.go` owns CLI arguments. `DCBAdapter` builds only `./cmd/dcb`
and runs `dcb up --workspace-folder ...`. It passes no option that upstream
`up` lacks: `--image-name` or `--label` would make dcb build a derived image
for an image-only configuration. VS Code server injection remains omitted.
BuildKit uses the Docker daemon unless `BUILDKIT_HOST` selects a separate
existing daemon. Fixture preparation, observation, normalization, and scoring
are shared with the upstream adapter.

Teardown selects only this run's labels, rechecks full Docker IDs and ownership,
then removes containers and labelled images, including after normal errors or
interrupts. Each workspace folder is named `<run>-<fixture>-<side>`, so Compose
project names and upstream derived image names are unique to the run and side.
Cleanup also removes image tags, Compose networks and Compose volumes whose
name or project contains that run prefix, and the `dcb-<hash>:latest` tag dcb
derives from each workspace path; image tags are removed without force.
Pulled base images and BuildKit cache remain available. Hard kills cannot run
cleanup; use the unique label recorded in `run.log` to identify
residue. Discovery regressions create stopped `alpine:3.20` containers through
Docker, skip when Docker is unavailable, and retain the pulled base image. Logs contain fixture output and local paths; review before sharing.

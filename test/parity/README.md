---
relationships:
  references: ../../reference/cli/package.json
---

# Parity harness

Run `git submodule update --init --depth 1`, then `task parity` from the repository
root. Docker with buildx, Go, Node and npx are needed. The upstream executable is
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

The three curated upstream fixtures cover an image, a Dockerfile and two local
v2 features. Each run copies them into separate workspaces under `.tmp/` and
adds a unique teardown label to runtime arguments and Dockerfile build options.
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

Normalization replaces observed container/image IDs, hostnames and temporary
workspace paths. Result timestamps are ignored. Environment and label maps and
mount sets are unordered; metadata and command arrays retain their order.
Unexpected environment values, metadata differences and missing results remain
visible. The current runtime has no JSON success result and will fail that
dimension. Do not manufacture one from inspect output.

`Adapter` in `adapter.go` owns CLI arguments. `LegacyAdapter` builds the current
`dcb` and `devcontainer-docker` binaries, uses buildx with VS Code server injection
omitted, then starts the built image. Replace it with a new adapter for the
single `dcb` CLI; fixture preparation, observation and scoring need no changes.

Teardown selects only this run's labels, rechecks full Docker IDs and ownership,
then removes containers and labelled images, including after normal errors or
interrupts. Pulled base images and BuildKit cache remain available. Hard kills
cannot run cleanup; use the unique label recorded in `run.log` to identify
residue. Logs contain fixture output and local paths; review before sharing.

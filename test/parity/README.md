---
relationships:
  references: [../../reference/cli/package.json, ../../docs/audits/upstream-options]
---

# Parity harness

Run `git submodule update --init --depth 1`, then `task parity` from the repository
root. Docker with buildx (for upstream), Go, Node and npx are needed.
The upstream executable is
resolved with `npx --yes @devcontainers/cli@<version>` using the checked-out
`reference/cli/package.json`, independent of any global CLI installation.
Missing Node/npx or Docker skips fixture comparison and retains option coverage.
A missing CLI submodule produces an incomplete option report and a nonzero exit.
The live harness is separate from `task check` and `go test ./...`.

Reports are written to `.tmp/parity/score.json`, `.tmp/parity/report.md` and
`.tmp/parity/run.log`. Override with `task parity -- --output <directory>`.
The JSON includes both normalized observations for each fixture and dimension.
The fixture score divides matched dimensions by all fixture dimensions (0 to
1). Failed CLI calls and unavailable observations count as failures, including
when both sides fail. Parity differences do not make the command fail; harness
setup, report and cleanup errors do. A skipped fixture run has zero fixture
dimensions and a `skipped` reason; it is not a fixture baseline score. Its
independent option coverage can be complete.

## Option coverage

`task parity` also prints registered-option coverage. Run
`task parity -- --options-only` to measure it without Docker, Go-built subprocesses,
Node or npx. Go still builds the harness itself. Both report formats include
coverage even when runtime prerequisites skip the fixture comparison.

The denominator comes from the checked-out upstream command registrations and
yargs option tables, including shared builders, nested commands and hidden flags.
Each command/long-option pair counts once. Inherited help/version count on the
root, groups and leaf commands; aliases are recorded but do not add score units.
Absent commands contribute uncovered options. Approved design omissions stay in
the raw denominator. The numerator comes from the real Cobra registrations,
including inherited and hidden flags. No hand-maintained option list drives
either number. `optionCoverage.complete` is true only after a nonempty inventory
is read and measured; a missing or unsupported source table writes an incomplete
report and makes the command fail. The source reader accepts the pinned upstream
declarative table shape and fails on unrecognized registrations or table entries;
a reference update can require a reader change.

This dimension measures registration, not supported values or behavior. A
registered flag with incomplete semantics can count as covered here and still
require implementation in the [option audit](../../docs/audits/upstream-options.md).
The audit lists all command options, dispositions, aliases, positional arguments,
approved omission reasons, sibling ownership and sized implementation units.
The fixture and option scores remain separate so their denominators are visible.

## Fixture comparison

The curated upstream fixtures cover an image, a Dockerfile, two local v2
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

Three repository fixtures under `fixtures/dockerfile-context/` build a
Dockerfile that copies its whole build context. The context is the folder that
holds `devcontainer.json`, its parent, or a subfolder, and each context has a
`.dockerignore` that excludes one file. The Dockerfile writes the list of
copied files to `/context-files.txt`; the lifecycle probe reads that list and
`/context/kept.txt`. A fixture's `Source` names the repository folder of a
fixture that is not in `reference/cli`.

The `additional-features` fixture runs the image fixture again and passes
`--additional-features` with the published common-utils feature to both
sides' `up`. A fixture's extra `up` arguments follow the workspace on both
command lines.

Dimensions compare the CLI JSON result, image `devcontainer.metadata`, container
environment, labels, mounts, user, working directory, lifecycle marker contents,
and generated feature executables (contents, executable permission and output).
Lifecycle probes read `/postCreateCommand.txt`; feature probes run `hello` and
`color`. Fixtures with no applicable probes produce empty maps. Observations
still inspect an existing container after a lifecycle failure.

The `user` dimension compares the effective UID and primary GID measured with
`docker exec <container> id -u` and `id -g`, using the container's default user.
It does not use the remote user or override the execution user. Each user
observation retains the inspected `Config.User` string in `info.rawUser`,
including an empty string; this information does not affect the score. An
unavailable user probe counts as a failure, even if both sides fail.

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
residue. Discovery regressions create running `alpine:3.20` containers through
Docker, skip when Docker is unavailable, and retain the pulled base image.
Logs contain fixture output and local paths; review before sharing.

---
title: Overview
order: 1
docs: true
relationships:
  references: [ install, usage, configuration, library ]
---

`dcb` builds and runs [development containers](https://containers.dev/) from a
`devcontainer.json`. It reads the same configuration as the
[Dev Container CLI](https://github.com/devcontainers/cli), installs the same
OCI features, and runs the same lifecycle commands.

- **Build.** `dcb build` turns a configuration into an image. It builds on the
  Docker daemon's built-in BuildKit, installs each feature as a separate
  cached step, and can load, push, or export the result for one or more
  platforms.
- **Run.** `dcb up` builds the image when needed, then creates and starts the
  container and runs the lifecycle commands. `dcb exec` runs a command in it,
  and `dcb down` removes it.
- **Read.** Every command except help and `exec` writes one JSON result line
  to stdout. Logs go to stderr. Exit codes separate operation failures from
  invalid invocations.

## Relation to the Dev Container CLI

`dcb` is a separate implementation of the
[Dev Container specification](https://containers.dev/implementors/spec/). The
goal is feature parity with the upstream `devcontainers/cli`: the same
configuration gives the same image and the same container. Command names and
flags follow upstream where the concept is the same.

Containers carry the upstream labels (`devcontainer.local_folder`,
`devcontainer.config_file`) and lifecycle marker files, so upstream tools and
editors find a container that `dcb` created, and the other way around.

Parity is measured, not assumed. The repository's parity harness runs both
CLIs on the upstream test fixtures and scores the matching results: the JSON
result, image metadata, container environment, labels, mounts, user, working
directory, and lifecycle and feature output. See
[the harness](https://github.com/wyrd-company/devcontainer-builder/tree/main/test/parity).
Some upstream behavior is not yet matched.

Differences from upstream:

| `dcb` | Upstream CLI |
| --- | --- |
| One static binary. | Node.js package. |
| Builds through the Docker Engine API on the daemon's BuildKit. | Runs the `docker` and `docker buildx` commands. |
| One JSON result line for every command except help and `exec`. | Output format varies by command. |
| Docker only. | Docker, or Podman through `--docker-path`. |
| Can install the VS Code server into the image at build time. | The editor installs the server when it attaches. |
| Adds `down`, `inspect`, `generate`, `server`, and `skills`. | Adds `outdated`, `upgrade`, and `templates`. |
| `dcb features` stages the features of a configuration. | `features` tests, packages, and publishes features. |

## Requirements

`dcb` needs a Docker Engine that it can reach with the standard `DOCKER_HOST`
and related environment variables. The `docker` command is not needed. Both
the containerd image store and the classic image store work. Podman is not
supported.

Builds use the daemon's built-in BuildKit. To use a separate BuildKit, set
`BUILDKIT_HOST` or pass `--buildkit-addr`. See
[Docker and BuildKit](/docs/dcb/configuration#docker-and-buildkit).

Release binaries are for Linux and macOS on `amd64` and `arm64`.

## Libraries

The Go packages behind `dcb` can be used directly: build an image, run a
container against the Docker Engine, or project a configuration into a
Kubernetes Pod. See [Go libraries](/docs/dcb/library).

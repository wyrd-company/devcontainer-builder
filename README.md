# dcb

![dcb logo](docs/user/assets/logo.svg#gh-light-mode-only)
![dcb logo](docs/user/assets/logo-dark.svg#gh-dark-mode-only)

`dcb` builds and runs [development containers](https://containers.dev/) from a
`devcontainer.json`. It builds images on the Docker daemon's built-in BuildKit,
and creates, starts, and runs lifecycle commands in containers through the
Docker Engine API. Every command writes one JSON result line to stdout, except
help, a valid `exec` invocation, and archive exports to stdout.

Documentation is at [wyrd.foo/docs/dcb](https://wyrd.foo/docs/dcb), published
from [`docs/user/`](docs/user).

## Install

| Method | Command |
| --- | --- |
| Homebrew | `brew install wyrd-company/tools/dcb` |
| npm | `npm install --global @wyrd-company/dcb` |
| APT | `sudo apt install dcb` |
| RPM | `sudo dnf install dcb` |
| AUR | `paru -S dcb-bin` |
| Go 1.26+ | `go install github.com/wyrd-company/devcontainer-builder/cmd/dcb@latest` |

APT and RPM need the [repo.wyrd.foo](https://repo.wyrd.foo) package
repository. [Installation](https://wyrd.foo/docs/dcb/install) gives the setup
commands and the release archives.

`dcb` runs on Linux and macOS, on `amd64` and `arm64`. Container operations need
a Docker Engine reachable through the standard `DOCKER_HOST` environment; the
`docker` command is not needed. Local Feature packaging does not need Docker.
Podman is not supported.

## Use

```bash
dcb up --workspace-folder .
dcb exec --workspace-folder . -- sh -c 'id && pwd'
dcb down --workspace-folder .
dcb features package ./src --output-folder ./packaged
dcb features generate-docs -p ./src -n example/collection
```

```json
{"containerId":"3f1c…","outcome":"success","remoteUser":"vscode","remoteWorkspaceFolder":"/workspaces/project"}
```

Build an image and push it for two platforms:

```bash
dcb build --workspace-folder . \
  --image-name registry.example.com/team/dev:1.0 \
  --platform linux/amd64 --platform linux/arm64 \
  --push
```

`dcb --help` lists every command. Exit code `0` is success, `1` is an
operation failure, and `2` is an invalid invocation, with a JSON error result.
After `exec` starts the container command, it passes the command's output
through and returns its exit code. See [Usage](https://wyrd.foo/docs/dcb/usage)
and [Configuration](https://wyrd.foo/docs/dcb/configuration).

`dcb skills install` writes an agent skill that describes these commands for
coding agents.

## Relation to the Dev Container CLI

`dcb` is a separate implementation of the
[Dev Container specification](https://containers.dev/implementors/spec/), with
the goal of feature parity with
[`devcontainers/cli`](https://github.com/devcontainers/cli). It uses the same
container labels and lifecycle marker files, so each tool finds containers that
the other created. Command names and flags follow upstream where the concept is
the same.

The [parity harness](test/parity) runs both CLIs on upstream fixtures and
reports the share of results that match. Some upstream behavior is not yet
matched. [Overview](https://wyrd.foo/docs/dcb/overview) lists the differences.

### Not yet supported

These upstream command families are not yet supported:

- [`outdated`](https://github.com/wyrd-company/devcontainer-builder/issues/20)
- [`upgrade`](https://github.com/wyrd-company/devcontainer-builder/issues/21)
- [Feature authoring commands other than `features package` and `features generate-docs`](https://github.com/wyrd-company/devcontainer-builder/issues/22)
- [Template commands other than `templates metadata`](https://github.com/wyrd-company/devcontainer-builder/issues/23)

## Go libraries

The packages under [`pkg/`](pkg) build images, run containers on Docker or
Kubernetes, and convert configurations into Kubernetes Pods. They can be
embedded in a service or an operator. See
[Go libraries](https://wyrd.foo/docs/dcb/library).

## Development

See [CONTRIBUTING.md](CONTRIBUTING.md). `task check` runs the checks that a
change must pass; `task parity` runs the parity harness.

## License

[MIT](LICENSE). The npm launcher and some release scripts are adapted from
other Wyrd Company projects under the Apache License 2.0; see
[`npm/NOTICE`](npm/NOTICE).

# dcb

![dcb logo](docs/user/assets/logo.svg#gh-light-mode-only)
![dcb logo](docs/user/assets/logo-dark.svg#gh-dark-mode-only)

`dcb` builds and runs [development containers](https://containers.dev/) from a
`devcontainer.json`. It builds images on the Docker daemon's built-in BuildKit,
and runs host initialization on up. It creates, starts, and runs lifecycle
commands in containers through the
Docker Engine API. Commands other than `outdated`, `features info`, and
`features resolve-dependencies`, and `upgrade` write one JSON result line to
stdout, except
help, a valid `exec` invocation, and archive exports to stdout.

Dockerfile build network and extra host options map to native BuildKit controls.

Documentation is at [wyrd.foo/docs/dcb](https://wyrd.foo/docs/dcb), published
from [`docs/user/`](docs/user).

Build inputs support local environment and workspace variable substitution.
Library callers select the environment with `builder.Options.LocalEnv`.

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
dcb features info verbose ghcr.io/example/collection/sample:1
dcb templates generate-docs -p ./templates
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

- [Feature authoring commands other than `features package`, `features generate-docs`, and `features info`](https://github.com/wyrd-company/devcontainer-builder/issues/22)
- [Template commands other than `templates metadata` and `templates generate-docs`](https://github.com/wyrd-company/devcontainer-builder/issues/23)

`dcb outdated` reports current, wanted, and latest OCI Feature versions as a text
table or with `--output-format json`. It does not need Docker or change lockfiles.

## Developers

The packages under [`pkg/`](pkg) build images, run containers on Docker or
Kubernetes, and convert configurations into Kubernetes Pods. They can be
embedded in a service or an operator. Image-source Kubernetes projection supports
command overrides and configuration-over-Feature environment precedence. See
[Go libraries](https://wyrd.foo/docs/dcb/library).

Dockerfile `build.options` supports native BuildKit cache bypass, image pulling,
networking, image labels,
build arguments, generated Dockerfile target selection, cache imports/exports,
build secrets, SSH session forwarding, shared memory size and build resource limits.
Other build option mappings remain explicit limitations; see
[configuration conformance](docs/audits/schema-conformance.md).

## Development

See [CONTRIBUTING.md](CONTRIBUTING.md). `task check` runs the checks that a
change must pass; `task parity` runs the parity harness.

## License

[MIT](LICENSE). The npm launcher and some release scripts are adapted from
other Wyrd Company projects under the Apache License 2.0; see
[`npm/NOTICE`](npm/NOTICE).

Kubernetes image-source `runArgs` maps native boolean assignments, combined and
attached short forms, stdin and TTY. Bare environment keys use `LocalEnv` (nil
uses the process environment); explicit empty maps disable inheritance. Final unset
keys require `ImageConfig`; removing an image-owned variable returns an error.
Merged security flags follow `runArgs`; merged security options use first-seen
union order. Detach conflicts with inherited output
attachment, and enabled init has no PodSpec equivalent. Other Docker flag and
remaining Compose translations remain separate coverage.

Kubernetes `ProjectCompose` primary command projection defaults `overrideCommand`
to false. Its shell wrapper runs Feature entrypoints in order, then the service
entrypoint and command. Absent service values fall back to the image; an explicit
empty entrypoint suppresses both image Entrypoint and Cmd, and an explicit empty
command suppresses image Cmd. Configuration `containerEnv` overrides ordered
Features, then service environment, above inherited image environment. Dollars
remain literal. Full Compose `runArgs` needs separate checks.

Kubernetes Compose sidecars preserve service command and entrypoint defaults,
including explicit empty overrides and the `[""]` entrypoint reset.
`ComposeProjectRequest.SidecarImageConfigs`
supplies native OCI image configuration by service name when an empty command
needs image entrypoint fallback or an unset environment key needs checking.
An explicit empty entrypoint suppresses image Entrypoint and Cmd; a service with
no executable returns a contextual error. Unset image-owned environment values
return an error because Pod environment omission inherits image values. Explicit
empty environment values remain empty. Authored command and environment dollars
remain literal. Sidecars receive no primary configuration or Feature overlays.

Native Docker Compose defaults `overrideCommand` to false. The primary shell
wrapper always runs ordered Feature entrypoints, then the preserved service or
image command. True uses the wrapper with no original command. The wrapper does
not enable stdin or TTY. Empty service entrypoints suppress image Entrypoint and
Cmd; empty commands suppress image Cmd. Sidecars use Engine's native entrypoint
reset, including `[""]`; the primary preserves that argument inside the pinned
upstream wrapper. Image inspection for command fallback is needed only when the
service entrypoint is absent.

Configuration `containerEnv` wins over ordered Features, then service values,
above image inheritance. Unset service environment keys remove image variables;
empty values stay empty. Generated override YAML escapes dollars for Compose
interpolation; direct runtime values retain literal dollars. `remoteEnv` applies
to exec and lifecycle commands. These checks cover native host command and
environment handling; full Compose and schema conformance remain separate work.

Image-source Docker containers always run the pinned shell wrapper, including
`overrideCommand: false` without Features. That mode appends the inspected image
Entrypoint and Cmd. The image stop signal remains inherited unless `runArgs`
overrides it. Authored `--entrypoint` values are parsed and replaced by the
wrapper on Docker and Kubernetes. Docker `--rm` uses native Engine automatic
deletion. Kubernetes reports enabled `--rm` as unsupported because a standalone
Pod has no equivalent deletion behavior; a final `--rm=false` is accepted.

Create containers before their first start with `dcb up --no-start`, then bind
external resources and run ordinary `dcb up`. The Go hosts expose the same mode
through `UpRequest.NoStart`. See [usage](docs/user/usage.md#create-before-first-start).

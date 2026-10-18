---
title: Configuration
order: 4
docs: true
relationships:
  references: [ usage ]
---

`dcb` reads `devcontainer.json` as defined by the
[Dev Container specification](https://containers.dev/implementors/json_reference/).
Comments and trailing commas are allowed.

## Container sources

| Source | Properties |
| --- | --- |
| Image | `image` |
| Dockerfile | `build.dockerfile` (or `dockerFile`), `build.context`, `build.args`, `build.target`, `build.cacheFrom` |
| Docker Compose | `dockerComposeFile`, `service`, `runServices` |

Features are installed on top of each source. With Docker Compose, `dcb`
creates the networks, volumes, and containers of the Compose project through
the Docker Engine API and runs the lifecycle commands in the `service`
container. The project uses the standard Compose labels, so `docker compose`
sees it.

## Supported properties

| Property | Notes |
| --- | --- |
| `name` | |
| `features`, `overrideFeatureInstallOrder` | Features from OCI registries and local paths. Each feature installs as a separate cached build step. |
| `remoteUser`, `containerUser` | |
| `updateRemoteUserUID` | See [Remote user UID](#remote-user-uid). |
| `containerEnv`, `remoteEnv` | `containerEnv` is set on the container; `remoteEnv` applies to lifecycle commands and `dcb exec`. |
| `workspaceFolder`, `workspaceMount` | The default mounts the local workspace at `/workspaces/<folder name>`. |
| `mounts` | |
| `init`, `privileged`, `capAdd`, `securityOpt` | |
| `runArgs` | See [runArgs](#runargs). |
| `appPort` | A bare port is published on `127.0.0.1`. A `host:container` entry is used as given. |
| `forwardPorts` | Not published, as upstream. |
| `overrideCommand` | |
| `onCreateCommand`, `updateContentCommand`, `postCreateCommand`, `postStartCommand`, `postAttachCommand` | String, array, and object forms. Object entries run in parallel. |
| `customizations` | Kept in the configuration and image metadata. |
| `shutdownAction` | Validated. `dcb down` always stops and removes the container. |

`initializeCommand` does not run: `dcb` runs no commands on the host.

### Variables

Strings can use `${localEnv:NAME}`, `${localWorkspaceFolder}`,
`${localWorkspaceFolderBasename}`, `${containerWorkspaceFolder}`,
`${containerWorkspaceFolderBasename}`, `${containerEnv:NAME}`, and
`${devcontainerId}`. A default value follows a colon, as in
`${localEnv:NAME:default}`. `dcb read-configuration` shows the configuration
after substitution.

### runArgs

`runArgs` is read as `docker run` flags and applied to the container through
the Engine API. Flags that add to a property, such as `--cap-add`, add to it
and do not replace it. An unknown flag is an error, so that a typing error does
not change the container. `--entrypoint` is an error, because `dcb` sets the
entrypoint that keeps the container running.

## Image metadata

`dcb build` and `dcb up` write the `devcontainer.metadata` label on the image,
as upstream does. The label records the configuration and the contribution of
each feature: lifecycle commands, mounts, capabilities, environment, users,
and customizations. When `dcb up` starts a container from an image with the
label, it merges the label with the current configuration by the upstream
rules. An image built once can then be started from a configuration that only
names the image.

`dcb read-configuration --include-merged-configuration` adds the merged
configuration to the result.

## Docker and BuildKit

`dcb` connects to Docker with the standard environment: `DOCKER_HOST`,
`DOCKER_TLS_VERIFY`, `DOCKER_CERT_PATH`, and `DOCKER_API_VERSION`.

Builds run on the Docker daemon's built-in BuildKit, with both the containerd
and the classic image store. To build on a separate BuildKit, set
`BUILDKIT_HOST` or pass `--buildkit-addr`; the flag has priority:

```bash
BUILDKIT_HOST=tcp://buildkit.example.com:1234 \
  dcb build --workspace-folder . \
  --image-name registry.example.com/team/dev:1.0 --push
```

## VS Code server

`dcb` does not install the VS Code server unless you ask. With
`--vscode-server`, `build`, `up`, and `generate` add the server to the image
at `~/.vscode-server/bin/<commit>/` of the remote user, so an editor that
attaches finds it in place. The download is checked against its digest and
cached by BuildKit.

| Flag | Meaning |
| --- | --- |
| `--vscode-commit` | The server commit. The default is the latest release. |
| `--vscode-quality` | `stable` (the default) or `insider`. |
| `--vscode-platform` | The server platform, when it differs from the image platform. |
| `--vscode-alpine` | The musl build, for Alpine images. |

## Remote user UID

On Linux and macOS, `dcb up` changes the UID and GID of `remoteUser` in the
container to those of the local user, so files in the workspace mount keep
the right owner. It does not change `root`, a numeric user, a user whose
IDs already match, or IDs that another user or group has.

`--update-remote-user-uid-default` sets the behavior when the configuration
has no `updateRemoteUserUID`:

| Value | Behavior |
| --- | --- |
| `on` | The default. Changes the IDs unless the configuration sets `updateRemoteUserUID: false`. |
| `off` | Changes the IDs only when the configuration sets `updateRemoteUserUID: true`. |
| `never` | Never changes the IDs. |

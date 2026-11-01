---
title: Configuration
order: 4
product: dcb
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
| Dockerfile | `build.dockerfile` with `build.context`, or `dockerFile` with `context`; `build.args`, `build.target`, `build.cacheFrom` |
| Docker Compose | `dockerComposeFile`, `service`, `runServices` |

Dockerfile paths and context paths are relative to `devcontainer.json`. Without
a context, the Dockerfile directory is the build context. Top-level `dockerFile`
can use `build.args`, `build.target`, and `build.cacheFrom`. `dockerComposeFile`
and `build.cacheFrom` accept a string or an array of strings. Lifecycle command
objects accept string or string-array values. Nested command objects are invalid.

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
| `remoteUser`, `containerUser` | See [Container and remote users](#container-and-remote-users). |
| `updateRemoteUserUID` | See [Remote user UID](#remote-user-uid). |
| `containerEnv`, `remoteEnv` | `containerEnv` is set on the container; `remoteEnv` applies to lifecycle commands and `dcb exec`. |
| `workspaceFolder`, `workspaceMount` | The default mounts the local workspace at `/workspaces/<folder name>`. With Docker Compose, the Compose file owns workspace mounts; no workspace bind is added automatically. `workspaceFolder` selects where commands run and defaults to `/`; an explicit `/` is supported. |
| `mounts` | |
| `init`, `privileged`, `capAdd`, `securityOpt` | |
| `runArgs` | See [runArgs](#runargs). |
| `appPort` | A bare port is published on `127.0.0.1`. A `host:container` entry is used as given. |
| `forwardPorts` | Not published, as upstream. |
| `overrideCommand` | |
| `onCreateCommand`, `updateContentCommand`, `postCreateCommand`, `postStartCommand`, `postAttachCommand` | String, array, and object forms. Object entries run in parallel. |
| `customizations` | Kept in the image metadata and collected in the merged configuration. |
| `shutdownAction` | Validated. `dcb down` always stops and removes the container. |

`initializeCommand` does not run: `dcb` runs no commands on the host.

### Variables

Strings can use `${localEnv:NAME}`, `${localWorkspaceFolder}`,
`${localWorkspaceFolderBasename}`, `${containerWorkspaceFolder}`,
`${containerWorkspaceFolderBasename}`, `${containerEnv:NAME}`, and
`${devcontainerId}`. A default value follows a colon, as in
`${localEnv:NAME:default}`. `dcb read-configuration` shows the configuration
after substitution.

### Feature containerEnv

A feature's `containerEnv` is applied as the reference CLI applies it: each
key becomes a Dockerfile `ENV` instruction before the feature installs. Keys
that are array indices, such as `1` or `10`, come first in numeric order; the
other keys follow in the order the feature declares them. This is the
JavaScript `Object.keys` order, so `01` and `4294967295` are not array indices.
A value reads the environment as built with `$NAME`, `${NAME}`, and the
Dockerfile modifiers such as `${NAME:-default}`: the base image's environment,
the earlier features, and the keys applied before it. `PATH` reads as the
platform default when the base image sets none, and an unset name reads as an
empty string. A substitution the Dockerfile rejects, such as
`${localEnv:NAME}` in a feature value, fails the build. Later install steps
and the final image get the expanded values, so the Go feature's
`PATH=/usr/local/go/bin:/go/bin:${PATH}` keeps the base image's `PATH`.

The configuration's own `containerEnv` is not expanded this way. It is applied
after the features, with its `${...}` references kept as written once the
variables above are substituted.

### runArgs

`runArgs` is read as `docker run` flags and applied to the container through
the Engine API. Flags that add to a property, such as `--cap-add`, add to it
and do not replace it. An unknown flag is an error, so that a typing error does
not change the container. `--entrypoint` is an error, because `dcb` sets the
entrypoint that keeps the container running.

`--name sample-dev` and `--name=sample-dev` set the Docker container name.
Without `--name`, Docker chooses the name. dcb finds and reuses containers by
identity labels. To replace an existing container with the configured name, use
`dcb up --remove-existing-container`. A name held by another container is an error;
dcb does not remove that container.

### mounts

Mounts accept strings or objects. Objects have only `type` (`bind` or `volume`),
`source`, and `target`. The `type` and `target` strings are required; `source`
is optional. A plain object mount is read-write:

```json
{
  "mounts": [
    { "type": "bind", "source": "/host/data", "target": "/data" },
    { "type": "volume", "source": "sample-cache", "target": "/cache" }
  ]
}
```

On the Docker host, put `,readonly` in the object's `target` to make the mount
read-only. As upstream does, dcb converts the object to Docker mount arguments,
so `/data,readonly` means a read-only mount at `/data`. The `/data,ro` form
works too:

```json
{
  "mounts": [
    { "type": "bind", "source": "/host/data", "target": "/data,readonly" },
    { "type": "bind", "source": "/host/shared", "target": "/shared,ro" }
  ]
}
```

The equivalent string form is:

```json
{
  "mounts": ["type=bind,source=/host/data,target=/data,readonly"]
}
```

Separate object properties such as `readOnly` or `readonly` are not in the
Dev Container specification. dcb rejects them with an error; use the `target`
option or string form above. Compose uses its own volume syntax; the Docker
comma-option example does not define Compose volume options.

## Image metadata

`dcb build` and `dcb up` write the `devcontainer.metadata` label on the image
they build when metadata entries are present, as upstream does. Empty metadata
adds no image label. A container created directly from an image receives `[]`
when it has no metadata entries; a Dockerfile build inherits its image label.
The label is a list of entries in order: the entries of the base image's own
label, one entry for each feature in install
order, and one for the configuration. An entry records lifecycle commands,
mounts, capabilities, security options, `init`, `privileged`, entrypoints,
environment, users, and customizations.

`dcb up` merges these entries with the current configuration by the upstream
rules:

- Lifecycle commands from every entry run, in entry order.
- `capAdd` and `securityOpt` combine. A later mount replaces an earlier mount
  with the same target.
- `init` and `privileged` are on when any entry turns them on.
- For other single values, such as `remoteUser`, the last entry that sets the
  value wins.

The container that `dcb up` creates carries the merged label as well. An image
built once can then be started from a configuration that only names the
image, and its features' lifecycle commands, mounts, and users still apply.

`dcb read-configuration --include-merged-configuration` adds the merged result
as `mergedConfiguration`. It reads the label of the workspace container, or
of the container given with `--container-id`. When there is no container, it
reads the base image configuration from Docker or the registry, without
building or pulling layers, and adds the features and the configuration. As
upstream, `mergedConfiguration` lists the lifecycle commands of all entries in
`onCreateCommands`, `updateContentCommands`, `postCreateCommands`,
`postStartCommands`, and `postAttachCommands`.

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

### Registry credentials

`dcb` uses the registry credentials of the Docker client configuration, as
the `docker` command does. It reads `config.json` in the `DOCKER_CONFIG`
directory, or in `~/.docker` when `DOCKER_CONFIG` is not set:

- `auths` entries, which `docker login` writes when there is no credential
  store.
- `credHelpers` and `credsStore`. `dcb` runs the named
  `docker-credential-<name>` program from `PATH` to get the credentials.
- `DOCKER_AUTH_CONFIG`, an environment variable that holds `auths` entries.

Programs that use the Go packages can give their own credential source in
place of this configuration. See
[Go libraries](/docs/dcb/library#registry-credentials).

These operations send the credentials:

- `dcb up` pulls of an image that Docker does not have.
- Builds: base images and `--push`.
- OCI features.
- Reads of the base image configuration for metadata and the feature user.

A registry with no credentials is accessed anonymously. When a credential
helper fails, image pulls, builds, and pushes fail, as they do with `docker`.
Feature downloads and base image configuration reads continue anonymously, as
they do with the Dev Container CLI. `dcb` keeps credentials in memory only,
and does not write them to logs or disk.

A `credsStore` or `credHelpers` value must be a plain program name suffix,
such as `desktop` or `ecr-login`; `dcb` finds the program on `PATH` only.
The output of a credential helper never appears in `dcb` output. Feature
downloads send credentials only to the registry and, over HTTPS, to the token
service that it names. They do not follow a redirect from HTTPS to HTTP, and
fail when the registry redirects to another origin that asks for
authentication.

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

## Container and remote users

Native builds preserve the base image's `USER`. Generated Dockerfiles preserve
it when the base image user can be resolved. If that lookup fails or image
metadata resolution is skipped, feature user defaults and the final generated
image `USER` fall back to `root`. A failed lookup also logs a warning.
`containerUser` sets the identity of PID 1 and feature entrypoints when the
container starts. Without `containerUser`, they use the image user.
`remoteUser` sets the identity of `dcb exec` and lifecycle commands; it does
not change the image user. Without `remoteUser`, these commands use the
container user.

A feature entrypoint that requires root, such as Docker-in-Docker, can start
as root while exec and lifecycle commands run as a non-root `remoteUser`.
Use a root image user or `containerUser: "root"` for that configuration.

## Remote user UID

On Linux, `dcb up` changes the UID and GID of `remoteUser` in the
container to those of the local user, so files in the workspace mount keep
the right owner. It does not change `root`, a numeric user, a user whose
IDs already match, or IDs that another user or group has.

On macOS, remapping is disabled by default, even with
`updateRemoteUserUID: true`. Pass `--update-remote-user-uid-default on`
explicitly to opt in. The configuration's `updateRemoteUserUID: false`
still disables remapping. Other host platforms do not remap.

On Linux, or after opting in on macOS, `--update-remote-user-uid-default`
sets the behavior when the configuration has no `updateRemoteUserUID`:

| Value | Behavior |
| --- | --- |
| `on` | The Linux default and explicit macOS opt-in. Changes the IDs unless the configuration sets `updateRemoteUserUID: false`. |
| `off` | On Linux, changes the IDs only when the configuration sets `updateRemoteUserUID: true`. Disables remapping on macOS. |
| `never` | Never changes the IDs. |

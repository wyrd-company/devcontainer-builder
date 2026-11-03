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

A common-only document, such as `{"remoteUser":"root"}`, can be read with
`dcb read-configuration`. Build and up require a container source.

## Container sources

| Source | Properties |
| --- | --- |
| Image | `image` |
| Dockerfile | `build.dockerfile` with `build.context`, or `dockerFile` with `context`; `build.args`, `build.target`, `build.cacheFrom` |
| Docker Compose | `dockerComposeFile`, `service`, `runServices` |

Scratch-only Dockerfiles are supported, including image configuration without
filesystem layers. Build steps that run commands still require those commands
to exist in the selected image.

Dockerfile paths and context paths are relative to `devcontainer.json`. Without
a context, the Dockerfile directory is the build context. Top-level `dockerFile`
can use `build.args`, `build.target`, and `build.cacheFrom`. `dockerComposeFile`
and `build.cacheFrom` accept a string or an array of strings. Lifecycle command
objects accept string or string-array values. Nested command objects are invalid.

`build.options` supports `--network default`, `--network none`, and
`--network host`, including `--network=value`. The last value wins; an empty
value uses the default network. These options control Dockerfile `RUN`
instructions. Host mode requests BuildKit's `network.host` entitlement; the
daemon must allow it. Custom container network names are not BuildKit build
networks. Image labels, build arguments, target selection, cache settings,
secrets, SSH sources, shared memory size, resource limits, cgroup parents and
extra hosts are supported, as described below. Build flags not documented as
supported return an explicit unsupported-option error.

Build options also accept repeated `--label KEY=VALUE` or `--label=KEY=VALUE`.
The last value for a key wins. Bare keys have empty values and do not inherit
the environment. Empty keys are invalid. Commas, equals signs in values, and
whitespace are retained using native buildx parsing. Labels reach the final image;
explicit library image labels take precedence.

An empty `dockerComposeFile` array selects discovery from the workspace folder.
Configuration and merged output retain the empty array.
Discovery uses `COMPOSE_FILE` from the supplied host environment, then the first
`COMPOSE_FILE=...` line in the workspace `.env` file, then `docker-compose.yml`
and an existing `docker-compose.override.yml` file. File lists in `COMPOSE_FILE`
use the host path delimiter. Values from `.env` are trimmed and otherwise literal.
Explicit file strings and nonempty arrays resolve from `devcontainer.json`.

`containerEnv` values must be strings. `remoteEnv` values must be strings or
null. Both properties must be objects. Remote null is retained in configuration
and becomes the literal string `"null"` in runtime commands, as in the pinned
upstream CLI. An empty string remains empty.

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
| `appPort` | Numeric values map to the same port on `127.0.0.1`. Strings use Docker publishing syntax unchanged; `"8080"` requests an allocated host port. |
| `forwardPorts` | Not published, as upstream. |
| `overrideCommand` | |
| `onCreateCommand`, `updateContentCommand`, `postCreateCommand`, `postStartCommand`, `postAttachCommand` | String, array, and object forms. Object entries run in parallel. |
| `customizations` | Kept in the image metadata and collected in the merged configuration. |
| `shutdownAction` | Validated. `dcb down` always stops and removes the container. |

`initializeCommand` runs on the host for every `dcb up`, before building or
starting containers, including with `--skip-post-create`. The working directory
is the local workspace folder. Commands inherit the host environment;
container and remote environment settings do not apply. Strings run through
`/bin/sh -c` (Windows: `ComSpec /c`, default `cmd.exe`); arrays execute directly.
Executable POSIX text scripts without a shebang use the native `/bin/sh` fallback.
Object entries run in parallel and buffer each named command's output until it
finishes. Empty commands do nothing. A failure aborts startup without cancelling
parallel siblings; SIGINT reports an interruption and allows startup to continue.
Initialization has no lifecycle marker. Build, generate, configuration reads,
set-up, exec and run-user-commands do not execute it. Command output uses the
lifecycle log stream; the CLI retains its JSON result on stdout.

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
not change the container. `--entrypoint` is accepted and replaced by the
keepalive entrypoint, as in the upstream CLI.

`--env-file` and `--label-file` use Docker file syntax. Files load in file order,
then explicit environment and label flags override them, regardless of argv
position. File paths are relative to the process working directory. Bare file
environment keys inherit host values when set; bare file label keys are omitted.
Merged `init`, `privileged`, `capAdd`, and `securityOpt` flags follow `runArgs`,
as in the upstream CLI.

Boolean assignments, combined short booleans such as `-it`, and attached short
values such as `-eSAMPLE=value` are accepted. `--rm`, stdin, and TTY settings reach
the Engine API. Bare environment keys inherit the host value; unset keys stay
bare so that Engine removes image defaults.

Like upstream, new containers inherit stdout and stderr attachment. `--attach`
accepts case-insensitive `stdin`, `stdout`, and `stderr`, including `-aSTDIN`.
Interactive mode also attaches stdin and sets close-on-disconnect behavior.
A final true `--detach` conflicts with the inherited attachments; `--detach=false`
is valid. The Engine client does not forward host process signals.

`--cidfile` reserves a client-side file before Engine creation and writes the
container ID without a newline before start. Existing files are left untouched
and stop creation. A failed create or write removes the unwritten reservation;
a written ID remains even if start fails. Repeated paths use the final value;
an empty final value disables the file. Relative paths use the process working
directory.

`--umask` uses Docker octal parsing and the final value, including explicit zero.
Creation confirms the value in daemon inspection before start. If the daemon
omits or changes it, dcb removes the new container and reports an error.
`--kernel-memory` validates Docker memory-size syntax and has no effect, as in
the native Docker CLI's deprecated flag.

Docker CPU run arguments preserve exact NanoCPU values through native Docker parsing.
`--cpu-period`, `--cpu-quota`, `--cpu-rt-period`, and `--cpu-rt-runtime`
retain Docker integer parsing and the final scalar value, including zero.
Daemon resource and platform validation still applies.

`--memory-swappiness` accepts Docker integer forms and final values of -1 or
0–100. `--blkio-weight`, `--cpu-shares`, `--pids-limit`, and `--oom-score-adj`
retain Docker integer parsing and final scalar values, including zero.
`--oom-kill-disable` uses Docker boolean spellings. Daemon resource validation
still applies.

Docker network flags preserve repeated attachments and native per-network CSV
options. Legacy `--ip`, `--ip6`, `--link-local-ip`, and network aliases apply
to the first attachment. Endpoint MAC addresses, links, driver options, and
gateway priority reach the Engine creation request. Conflicting legacy and
per-network values return errors. Address allocation and network enforcement
belong to the daemon.

Docker storage options (`--storage-opt`) retain key/value entries and the last
value for each key. Windows resource flags (`--cpu-count`, `--cpu-percent`,
`--io-maxbandwidth`, `--io-maxiops`) map to Engine creation fields. CPU and IOPS
values use Docker base-zero integers; bandwidth uses Docker memory-size units.
The daemon and platform determine whether these controls are supported.

Docker per-device block I/O flags (`--blkio-weight-device`,
`--device-read-bps`, `--device-write-bps`, `--device-read-iops`, and
`--device-write-iops`) use native Docker validation. Repeated entries retain
argument order, including repeated devices and zero values. Byte rates accept
Docker size units; IOPS values are decimal integers. The Docker daemon and host
kernel determine whether the requested limits can be enforced.

Docker `runArgs` health options (`--health-cmd`, `--health-interval`,
`--health-timeout`, `--health-start-period`, `--health-start-interval`, and
`--health-retries`) retain Docker values. `--no-healthcheck` disables image health
checks and conflicts with nonzero health options. Repeated scalar flags use the
last value. `--log-driver` and `--log-opt` preserve logging configuration; repeated
option keys use the last value. Docker rejects logging options for driver `none`.

`--name sample-dev` and `--name=sample-dev` set the Docker container name.
Without `--name`, Docker chooses the name. dcb finds and reuses containers by
identity labels. To replace an existing container with the configured name, use
`dcb up --remove-existing-container`. A name held by another container is an error;
dcb does not remove that container.

### mounts

Mounts accept strings or objects. Docker mount strings use Docker CSV syntax,
including quoted values, bind propagation, volume options, and tmpfs options.
A string without `type` defaults to a volume mount. The same parser applies to
`workspaceMount`, `mounts`, and `runArgs --mount`. Invalid options return an error.

Objects follow the upstream schema. Objects have only `type` (`bind` or `volume`),
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

Native builds without Features preserve the base image's `USER`. Feature builds
and generated Dockerfiles restore the inferred base image user, with explicit
`root` when the base image has no user. If that lookup fails or image
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

Build options accept repeated `--build-arg KEY=VALUE` and `--build-arg=KEY=VALUE`.
Arguments override `build.args`; explicit library build arguments apply last.
Bare keys inherit the host environment when set. Unset bare keys leave earlier
values intact; explicit empty values remain empty. Empty keys are invalid.
Metadata and image user inference use `build.args` independently of these options,
as in the pinned upstream CLI. Generated feature arguments retain their own
staging and final image handling.

Build options accept `build.options --target STAGE` and
`build.options --target=STAGE`; the last value wins.
These options select from the generated Dockerfile. An authored stage bypasses
feature installation, configuration environment, and generated metadata. The
`dev_containers_target_stage` target, or an empty target, keeps the final image
layers based on `build.target`. With Features, the
`dev_containers_feature_content_normalize` target exports the root-owned builtin
environment preparation stage. Metadata and user inference still use the
configured `build.target` independently of the option.

Build options accept repeated `build.options --cache-from` and
`build.options --cache-to` with native buildx CSV syntax, including quoted
values, typed backends and bare registry references. Invalid entries return
errors. Library cache imports precede `build.cacheFrom`, followed by option
imports. Library cache exports precede option exports. No-cache builds omit
library and configured imports but retain explicit option imports. Native
BuildKit sessions transfer local cache stores and use the existing registry
credentials. The default inline-cache build argument can be overridden through
`build.args`, build options or explicit library arguments. Backend availability
and export support depend on the BuildKit worker.

Build options accept repeated `build.options --secret` and `build.options --ssh`
with native buildx syntax. Secrets support file sources, environment sources and
implicit source selection by ID. Repeated secret IDs select the last source;
repeated SSH IDs return an error. SSH sources support agent sockets and native
private-key files; an omitted path uses `SSH_AUTH_SOCK`. Empty option values add
no source. Relative paths use the invoking host working directory. Native
BuildKit sessions supply these sources to Dockerfile builds, including generated
feature stages. Invalid sources return errors. These build sources are separate
from lifecycle `--secrets-file` values.

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

Build options accept `build.options --shm-size` with Docker memory-size syntax
and repeated `build.options --ulimit NAME=SOFT:HARD`. The last shared memory
size wins; zero uses the BuildKit worker default. Resource limits use native
Docker validation, shorthand single values and unlimited values. The last limit
for each name wins. Both Dockerfile solve routes pass these settings to the
native BuildKit frontend; worker enforcement remains platform-dependent.

Build options accept `build.options --no-cache` and `build.options --pull`
with native Docker boolean assignments and last-value precedence. Dockerfile
invocation no-cache enables both cache bypass and image pulling before these
options apply. Explicit false values can override those generated flags. Cache
import generation still uses invocation no-cache; option-only cache bypass
retains configured imports. These settings reach standalone and nested
Dockerfile solves.

Build options accept `build.options --cgroup-parent` as a native string value.
The last value wins; an explicit empty value restores the worker default.
Both Dockerfile solve routes pass nonempty values to the native frontend.
Cgroup availability and enforcement depend on the BuildKit worker.

Build options accept repeated `build.options --add-host` with native buildx
CSV slice syntax. Host mappings accept colon or equals separators, IPv4, IPv6,
optional address brackets and quoted multiple IP addresses. Repeated mappings
retain their order. `host-gateway` uses the selected Docker worker's advertised
host gateway IP; missing or invalid worker labels return an error. Separate
BuildKit endpoints follow the native remote driver's unsupported gateway
behavior. Both Dockerfile solve routes receive the native frontend mappings.

Build inputs resolve local environment and workspace variables before the build.
This includes image names, Dockerfile and context paths, build arguments, target,
cache sources, build options, and feature option values. Build-time environment
and feature users also resolve configuration variables. Configuration keys stay
literal. Explicit command-line or library build arguments apply last and stay
literal. Runtime contributions keep their authored values for the host phase.

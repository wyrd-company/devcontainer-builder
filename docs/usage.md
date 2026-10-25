---
title: Usage
order: 3
docs: true
relationships:
  references: [ configuration ]
---

## Quick start

Put a configuration in `.devcontainer/devcontainer.json`:

```json
{
  "image": "mcr.microsoft.com/devcontainers/base:ubuntu",
  "features": {
    "ghcr.io/devcontainers/features/common-utils:2": {}
  },
  "remoteUser": "vscode",
  "postCreateCommand": "echo ready > /tmp/ready"
}
```

Start the container, run a command in it, and remove it:

```bash
dcb up --workspace-folder .
dcb exec --workspace-folder . -- sh -c 'id && pwd'
dcb down --workspace-folder .
```

`dcb up` writes one result line:

```json
{"containerId":"3f1c…","outcome":"success","remoteUser":"vscode","remoteWorkspaceFolder":"/workspaces/project"}
```

## Commands

| Command | What it does |
| --- | --- |
| `dcb up` | Builds the image if the configuration needs it, creates and starts the container, and runs the lifecycle commands. An existing container for the workspace is started and reused. |
| `dcb exec` | Runs a command in the workspace container. |
| `dcb down` | Stops and removes the workspace container. |
| `dcb inspect` | Returns the Docker inspection of the workspace container. |
| `dcb build` | Builds the image and loads, pushes, or exports it. |
| `dcb read-configuration` | Returns the configuration with variables substituted. |
| `dcb run-user-commands` | Runs the lifecycle commands in the existing workspace container. |
| `dcb set-up` | Runs the lifecycle commands of a configuration in a container that `dcb` did not create. |
| `dcb generate` | Writes the generated Dockerfile and build context, and does not build. |
| `dcb features` | Resolves and downloads the configuration's features into a directory. |
| `dcb server` | Downloads a VS Code server archive. |
| `dcb skills` | Prints or installs the bundled agent skill. |
| `dcb version` | Returns the `dcb` version. `dcb --version`, or `--version` on any command, returns the same result in place of the command. |

`dcb --help` lists the commands with examples. `dcb <command> --help` lists
the flags of one command. Help is plain text.

Boolean flags accept a bare flag, an equals value, or a separated value:
`--prebuild`, `--prebuild=true`, `--prebuild=false`, `--prebuild true`, and
`--prebuild false`. Separated values must be lowercase `true` or `false`;
other values remain positional arguments. The no- prefix does not negate
boolean flags, as upstream. Arguments after `--` pass through unchanged.

### Not yet supported

These upstream command families are not yet supported:

- [`outdated`](https://github.com/wyrd-company/devcontainer-builder/issues/20)
- [`upgrade`](https://github.com/wyrd-company/devcontainer-builder/issues/21)
- [Feature authoring commands under `features`](https://github.com/wyrd-company/devcontainer-builder/issues/22)
- [`templates`](https://github.com/wyrd-company/devcontainer-builder/issues/23)

### Workspace flags

Commands that read a configuration take these flags:

| Flag | Meaning |
| --- | --- |
| `--workspace-folder` | The local workspace folder. The default is the current directory. |
| `--config` | The path to `devcontainer.json`. The default is discovery in the workspace folder: `.devcontainer/devcontainer.json`, then `.devcontainer.json`. |
| `--override-config` | A file with alternate configuration content. Relative paths in it resolve against the `--config` location. |

`dcb` identifies the container of a workspace by its labels, so `exec`,
`down`, `inspect`, and `run-user-commands` find the container that `up`
created. `exec`, `run-user-commands`, `set-up`, and `read-configuration` also
take `--container-id` to select a container directly. `--id-label` selects by
other labels; see [Id labels](#id-labels).

## Feature lockfiles

`build` and `up` accept the hidden boolean flags `--experimental-lockfile` and
`--experimental-frozen-lockfile`. Both default to false.

```bash
dcb build --workspace-folder . --experimental-lockfile
dcb up --workspace-folder . --experimental-frozen-lockfile
```

When features exist, `--experimental-lockfile` creates `devcontainer-lock.json`
next to the configuration. A dot-prefixed configuration such as
`.devcontainer.json` uses `.devcontainer-lock.json`. An existing lockfile,
including an empty initialization marker, updates by default after success.
The file records remote feature versions, resolved references, integrity digests,
and hard dependencies. Local features are omitted.

Resolution uses the existing integrity digest to pin OCI features, including
transitive dependencies. HTTPS feature archives must match their recorded
archive digest. `read-configuration --include-features-configuration` uses the
same pins and reports the resolved digest in each feature set.

Frozen mode fails if the file is missing or the resolved feature graph differs.
Whitespace changes do not count as drift. Frozen mode never writes, including
when both flags are set. To resolve a newer version of the same reference,
remove its lock entry before running a write-enabled build.

Publication uses a complete temporary file beside the destination, then an
atomic rename, only after the command succeeds. A failed or cancelled build or
lifecycle command leaves the existing lockfile unchanged.

## Results

Every command writes exactly one JSON object on one line to stdout, except
help, a valid `exec` invocation, and archive exports to stdout (see
[Build exporters](#build-exporters)). An invalid invocation, including one of
`exec`, writes a JSON error result and exits with `2`. The `outcome` field is
`success` or `error`. An error result also has `message` and `description`:

<!-- rumdl-disable MD013 -->

```json
{"description":"dcb could not complete the requested command","message":"docker host: container not found","outcome":"error"}
```

<!-- rumdl-enable MD013 -->

`build` and `up` remove staged build artifacts after success or failure.
Use `--keep` to retain them for inspection. A build error includes `contextDir`
only when `--keep` retains the context. An explicit `--work-dir` has the same
cleanup rules. `generate` retains its output folder.

Other result fields by command:

| Command | Fields |
| --- | --- |
| `up` | `containerId`, `remoteUser`, `remoteWorkspaceFolder`; `composeProjectName` for a Compose configuration |
| `inspect` | `container`, and the `up` fields |
| `run-user-commands` | `result`, `phasesRun`, and the `up` fields |
| `set-up` | `phasesRun`, and the `up` fields |
| `build` | `imageName`; `cacheOnly` for cache builds; `output` for explicit exports, plus `outputDir` for local exports |
| `generate` | `dockerfile`, `contextDir` |
| `read-configuration` | `configuration`, `workspace`; `mergedConfiguration` with `--include-merged-configuration`; `featuresConfiguration` with `--include-features-configuration` |
| `features` | `features` |
| `server` | `commit`, `quality`, `path`, `url`, `bytes` |
| `skills` | `skills`; `installed` for `skills install` |
| `version` | `version` |

`composeProjectName` follows the upstream CLI order: `COMPOSE_PROJECT_NAME` in
the host environment, then in the workspace's `.env`, then the Compose `name:`.
An authored `name: devcontainer` is retained. Without a name, a first Compose
file in `<workspace>/.devcontainer` uses `<workspace basename>_devcontainer`;
other locations use the basename of the first Compose file's directory.
Names are lowercase and retain `-`, `_`, letters, and digits. Workspace-only
operations find the running or stopped project's captured name through its
container labels, including when the configuration file has been removed.

Workspaces with the same basename can select the same Compose project. `up`
refuses by default when that project contains a container labelled for another
workspace. The text diagnostic lists every conflicting container's name, full
ID, and owning `devcontainer.local_folder`. The JSON error result includes
`composeProjectName` and a `projectConflicts` array with `name`, `id`, and
`localFolder` for each conflict.

Use `dcb up --allow-project-conflict --workspace-folder .` to allow upstream
behavior: containers in the project can be reused or recreated for this
workspace. The override logs a warning naming the conflicting containers. It
does not override the teardown refusal below.

Set a distinct top-level `name:` in each workspace's Compose file to keep their
projects separate, for example:

```yaml
name: sample-alpha
services:
  app:
    image: alpine:3.20
```

Use another name, such as `sample-beta`, for the other workspace. A
`COMPOSE_PROJECT_NAME` in the host environment or workspace `.env` takes
precedence over `name:`. The library's `Stop` and `Remove`, and CLI `down`, refuse
a project that contains a container labelled for another workspace, so that its
containers, networks, volumes, and materialized configuration files remain intact.

`phasesRun` lists the lifecycle commands that ran, in order. Each entry has
`hook`, the upstream name such as `postCreateCommand`, and `origin`, which is
`devcontainer.json` or the reference of the feature that contributed it.

A successful `run-user-commands` also returns `result`: `skipNonBlocking`,
`prebuild`, `stopForPersonalization`, or `done`. It identifies the stop point,
even when that hook has no command or markers skip the commands. At
`updateContentCommand`, `skipNonBlocking` takes precedence over `prebuild`;
`prebuild` takes precedence over personalization. Default execution returns
`done`. A failed lifecycle command returns the error outcome without `result`.

### Exit codes

| Code | Meaning |
| --- | --- |
| `0` | Success. |
| `1` | The operation failed. |
| `2` | The invocation is not valid, for example an unknown flag. |

A valid `exec` invocation returns the exit code of the container command.
See [Run a command](#run-a-command).

### Logs

Build output, lifecycle command output, and diagnostics go to stderr.
`--log-level` selects the diagnostics: `trace`, `debug`, `info` (the default),
`warn`, `error`, or `off`. Build and lifecycle output is not filtered.

`--log-format json` writes each stderr record as JSON. A diagnostic has
`type: diagnostic`, `level`, and `text`. Build and lifecycle output has
`type: output` and `text`.

## Run a command

`exec` stops reading `dcb` options at the container command. All later
arguments pass through unchanged. Both forms work:

```bash
dcb exec --workspace-folder . ls -la /workspaces
dcb exec --workspace-folder . -- ls -la /workspaces
```

`exec` has three outcomes:

- **The command runs.** `exec` writes no result line. The command's stdout and
  stderr pass through unchanged, and `dcb` exits with the command's exit code.
- **The invocation is not valid**, for example no command or an unknown flag.
  stdout has one JSON error result, stderr is empty at the default log level,
  and the exit code is `2`.
- **The operation fails** before or outside the command. stdout is empty,
  stderr has the error, and the exit code is `1`.

With `--tty`, the command runs on a pseudo-terminal, and its output arrives on
stdout.

`exec` runs as the configured `remoteUser` with `remoteEnv` applied. Use
`--user`, `--remote-env KEY=VALUE`, and `--remote-workspace-folder` to change
the user, environment, and working directory for one command. Configuration
`remoteEnv` replaces a `--remote-env` value with the same name, as upstream.
Values set only by `--remote-env` are added to the command environment.

A command can also exit with `1`. To tell this apart from an operation
failure, look for the `dcb` error diagnostic on stderr. In text mode
it starts with `dcb: error: `. In JSON mode it is a record with
`type: diagnostic` and `level: error`. The command can print the same text,
so this check is a heuristic.

## Lifecycle commands

`dcb up` runs `onCreateCommand`, `updateContentCommand`,
`postCreateCommand`, `postStartCommand`, and `postAttachCommand`, including
the ones that features contribute. Marker files in the container record the
commands that ran, so a later `up` on the same container does not repeat
creation commands.

- `--skip-post-create` skips all lifecycle commands.
- `--skip-post-attach` skips only `postAttachCommand`.
- `--remove-existing-container` removes the workspace container and creates a
  new one.

`dcb run-user-commands` runs the lifecycle commands in the existing
container. The markers skip the creation commands that already ran.
`dcb set-up --container-id <id>` applies the lifecycle commands of a
configuration to a container that was started some other way.

### Lifecycle controls

All lifecycle control flags are booleans and default to `false`.

| Flag | Commands | Meaning |
| --- | --- | --- |
| `--skip-non-blocking-commands` | `up`, `run-user-commands`, `set-up` | Stop after the merged configuration's `waitFor` hook, or `updateContentCommand` by default. Later hooks do not run in the background. |
| `--prebuild` | `up`, `run-user-commands` | Stop after `updateContentCommand`. Rerun that hook even when its creation marker is current; `onCreateCommand` still uses its marker. |
| `--stop-for-personalization` | `run-user-commands` | Run through `postCreateCommand` and the dotfiles install, then stop before `postStartCommand`. |
| `--expect-existing-container` | `up` | Fail before building or creating resources if the workspace container does not exist. A stopped container qualifies. |

The stop point applies even when that hook has no commands. All feature and
configuration commands through the stop point use their normal markers.
With `--prebuild`, all `updateContentCommand` contributions rerun.
When stop controls are combined, the first stop point wins. A `waitFor` stop
at `postCreateCommand` precedes dotfiles; personalization follows dotfiles.
A `waitFor` stop at `initializeCommand` skips all container lifecycle hooks.
`--skip-post-create` continues to skip all lifecycle commands and dotfiles.

`--expect-existing-container --remove-existing-container` checks that a
container exists, then replaces it. Without explicit removal, an expected
Compose container is reused without recreation when its configuration changes.

### Dotfiles

`up`, `run-user-commands`, and `set-up` can install a dotfiles repository
after `postCreateCommand` and before `postStartCommand`:

```bash
dcb up --workspace-folder . \
  --dotfiles-repository https://example.com/example-owner/dotfiles.git \
  --dotfiles-install-command install.sh
```

| Flag | Meaning |
| --- | --- |
| `--dotfiles-repository` | A Git URL or a path in the container. A value such as `example-owner/dotfiles`, without `:` and not starting with `/`, `./`, or `../`, is a GitHub repository. |
| `--dotfiles-install-command` | The command to run in the clone. Without it, `dcb` runs the first of `install.sh`, `install`, `bootstrap.sh`, `bootstrap`, `script/bootstrap`, `setup.sh`, `setup`, and `script/setup`. Without any of them, it links the repository's dot files into the home folder. |
| `--dotfiles-target-path` | The clone location. The default is `~/dotfiles`. The container shell expands it, and a relative path starts at the container's working folder. A location that exists is used as it is, without a clone, as in the upstream CLI. |

The clone and the install command run in the container as the remote user,
with the remote environment, so the container needs `git`. `dcb` clones
into a temporary folder beside the clone location and moves the clone into
place only when it is complete, so a command never uses the partial clone of
another command. After a successful install, `dcb` writes a marker file, so
a later command on the same container does not repeat it.
`--skip-post-create` also skips the dotfiles.

A failed install does not fail the command, as in the upstream CLI: the
result is `success` and stderr has a warning that names the repository. In
JSON log mode the warning is a record with `type: diagnostic` and
`level: warn`. The upstream CLI writes the marker before the install; `dcb`
writes it only after a successful install, so the next `up` installs again
after a failed or interrupted install. A missing `git` is a failure. If
`dcb` cannot write the marker, it gives a warning and the next `up` installs
again.

### Remote environment and secrets

`up`, `run-user-commands`, and `set-up` take `--remote-env KEY=VALUE`.
Repeat it for more variables. `up` and `run-user-commands` also take
`--secrets-file`, the path of a JSON file of secret variables:

```json
{"SAMPLE_TOKEN": "example-value"}
```

```bash
dcb up --workspace-folder . \
  --remote-env SAMPLE_MODE=test \
  --secrets-file ./secrets.json
```

The variables apply to the lifecycle commands and the dotfiles install of
this command only. `dcb` does not keep them: they are not in the container
configuration, in a label, or in a file, and a later `exec` does not get
them. The `configuration` and `mergedConfiguration` result fields do not
contain them. For these three commands, as in the upstream CLI, a configuration
`remoteEnv` value replaces a `--remote-env` value with the same name, and a
secret replaces both.

A `--remote-env` value with no `=`, or with no name before it, is an invalid
invocation. The secrets file can have comments. Each value is a string, a
number, or a boolean. As in the upstream CLI, a number or a boolean becomes
its JavaScript text, for example `1.0` becomes `1` and `true` stays
`true`. An object, an array, or `null` is not a valid value. If `dcb` cannot
read or parse the file, the command fails with exit code `1`, and the error
gives the path and the reason but not the content. `dcb` reads the file
once, so a pipe such as `/dev/stdin` works. An empty `--secrets-file` value
means no secrets, as in the upstream CLI.

`dcb` replaces each secret value with `********` in stderr and in the result,
as the upstream CLI does in its log. This includes the output of lifecycle
commands, Docker errors, and errors about other flags. `dcb` also replaces
the value's quoted, JSON-escaped, base64 (standard and URL, with and without
padding), and URL-escaped forms. A value that is changed in another way, or
that is part of a longer encoded text, is not replaced. A short value, such
as `1`, is replaced wherever that text appears. An invalid `--remote-env`,
`--build-arg`, or `--label` value is reported by its flag and position, not
by its text.

## Build an image

`dcb build` loads the image into Docker by default:

```bash
dcb build --workspace-folder . --image-name example:dev
```

Push to a registry instead. Repeat `--platform` for a multi-platform image:

```bash
dcb build --workspace-folder . \
  --image-name registry.example.com/team/dev:1.0 \
  --platform linux/amd64 --platform linux/arm64 \
  --push
```

| Flag | Meaning |
| --- | --- |
| `--image-name` | An image name. Repeat for more names. |
| `--push` | Pushes the image to its registry. |
| `--load` | Loads the image into Docker. This is the default when there is no `--push` or `--output`. `--load=false` builds into the cache only. |
| `--output` | A buildx exporter specification or local directory. See [Build exporters](#build-exporters). It cannot be combined with `--push` or `--load`. |
| `--platform` | A target platform, such as `linux/arm64`. Docker cannot load a multi-platform image; use `--push` or `--output`. |
| `--build-arg` | A Dockerfile build argument, `KEY=VALUE`. |
| `--label` | An image label, `KEY=VALUE`. |
| `--cache-from`, `--cache-to` | BuildKit cache import and export settings. Repeatable. |
| `--no-cache` | Disables the build cache. |
| `--pull` | Always pulls referenced images. |
| `--skip-features` | Builds without the configuration's features. |
| `--additional-features` | More features as JSON in the form of the configuration's `features` property. They follow the configuration's features. For a feature the configuration already names, the configuration's options are used. Invalid JSON is an invalid invocation. |
| `--vscode-server` | Installs the VS Code server into the image. See [VS Code server](/docs/dcb/configuration#vs-code-server). |
| `--progress` | Build progress format: `plain` (the default), `auto`, `tty`, or `rawjson`. |
| `--buildkit-addr` | Uses a separate BuildKit. See [Docker and BuildKit](/docs/dcb/configuration#docker-and-buildkit). |

`dcb up` takes the same build flags except the export flags, and uses
`--build-no-cache` in place of `--no-cache`. For example, add a feature to a
configuration without editing it:

```bash
dcb up --workspace-folder . \
  --additional-features '{"ghcr.io/devcontainers/features/common-utils:2":{}}'
```

`dcb read-configuration --include-merged-configuration` also takes
`--additional-features`. As in the upstream CLI, the features add their
metadata to `mergedConfiguration` only when the workspace has no container,
and `mergedConfiguration.features` lists only the configuration's features.

### Configuration results

`up` and `set-up` accept `--include-configuration` and
`--include-merged-configuration`. Both default to false. Enable either flag to
add `configuration` or `mergedConfiguration` to the success result. Configuration
values include workspace and container environment substitutions. The merged
result includes image and feature metadata, with collected lifecycle commands.
Lifecycle secrets are separate from these configuration snapshots.

```bash
dcb up --workspace-folder . --include-configuration --include-merged-configuration
dcb set-up --workspace-folder . --container-id sample-container --include-configuration
dcb read-configuration --workspace-folder . --include-features-configuration
```

`read-configuration` applies workspace and host substitutions to `configuration`.
When an existing container is selected by `--container-id` or workspace discovery,
its environment is substituted before producing configuration, merged output,
and resolved feature options, independently of the include flags. Without a
container, container environment references remain unresolved. Ordinary reads
without an explicit container ID also work when Docker is unreachable; discovery
and inspection operation errors are reported. Merged and feature output retain
their Docker requirements.

`read-configuration --include-features-configuration` resolves declared features,
additional features and their dependencies in installation order. Its optional
`featuresConfiguration` result contains `featureSets` with feature metadata,
option values and source information, plus the temporary staging `dstFolder`.
The field is omitted when there are no features. Resolved files remain in the
reported staging paths. Use `dcb features --out ./staged-features` to choose a
staging location.

### Inspect what a build uses

`dcb generate` writes the Dockerfile and build context for a configuration,
and `dcb features` downloads its features, without a build:

```bash
dcb generate --workspace-folder . --work-dir ./generated
dcb features --workspace-folder . --out ./staged-features
```

`dcb server` downloads a VS Code server archive:

```bash
dcb server --out ./server --platform linux/amd64
```

## Agent skills

`dcb` includes an agent skill that tells a coding agent how to use the
commands, results, and exit codes.

```bash
dcb skills --list
dcb skills dcb
dcb skills install --dir .agents/skills
```

`dcb skills dcb` returns the skill text in the result's `skills[].content`.
`dcb skills install` writes `dcb/SKILL.md` below the directory given with
`--dir`. The default is `.agents/skills`.

## Build exporters

`dcb build --output` accepts a short directory path or a CSV exporter specification.
Quote an attribute that contains commas, for example
`'type=image,"name=example.test/sample:one,example.test/sample:two"'`.
Exporter attributes pass to BuildKit. Available exporters and attributes depend
on its worker and the Docker image store.

| Value | Result |
| --- | --- |
| `results` or `type=local,dest=results` | Image filesystem in a local directory |
| `type=tar,dest=result.tar` | Image filesystem as a tar archive |
| `type=oci,dest=image.tar` | OCI image archive |
| `type=oci,tar=false,dest=layout` | OCI image layout directory |
| `type=docker,dest=image.tar` | Docker image archive through the Engine API on the Docker daemon |
| `type=docker` | Load into Docker |
| `type=image,name=example.test/sample:latest` | Export a named image |
| `type=registry,name=example.test/sample:latest` | Push a named image, using existing registry credentials |
| `type=image,name=example.test/sample:latest,push=true` | Push a named image, using existing registry credentials |
| `type=cacheonly` | Keep the build result in the build cache |

`--image-name` supplies the default image name when `name=` is absent.
`type=registry` is shorthand for the image exporter with `push=true`.
The Docker daemon path uses the Engine API for push and Docker archive export.
A separate BuildKit uses its native exporters. The classic Docker image store
does not provide the OCI exporter. Enable Docker's containerd image store or use
a separate BuildKit worker for OCI export.

`--output -` means a filesystem tar archive on stdout. `dest=-` also streams
archives to stdout; omitted destinations stream tar and OCI archives to stdout.
In these modes, stdout contains only archive bytes, with no JSON result line.
Archive output to an interactive terminal is refused with exit code 2 and a JSON
error result. Redirect stdout to a pipe or file, or set a file destination.
Errors and progress go to stderr. `type=docker` without a destination loads into
Docker and retains the JSON result line. Other explicit exports return `output`
in the JSON result; local exports also return `outputDir`.

Output files are opened when the exporter writes them. Existing output files can
be replaced, as with buildx. Local exports use BuildKit's directory merge behavior.

## GPU availability

`dcb up --gpu-availability detect` checks the Docker Engine NVIDIA runtime when
`hostRequirements.gpu` is true, `"optional"`, or an object. The default is `detect`.
When support is found, dcb requests all GPUs through Engine device requests.
Without support, creation continues; a required GPU produces a warning.

`--gpu-availability all` requests GPUs without detection when the configuration
requires them. Docker rejects creation if it cannot satisfy the request.
`--gpu-availability none` suppresses automatic GPU requests. Explicit GPU selection
in `runArgs` is preserved in every mode. For Compose, dcb adds a
`deploy.resources.reservations.devices` GPU reservation to the primary service
under the same availability rules. Explicit Compose GPU reservations and `gpus`
selection take precedence and are preserved in every mode. Detection uses the
Engine API without a host command.

## Data folders

`--user-data-folder` selects the host folder for CLI state on `build`, `exec`,
`read-configuration`, `run-user-commands`, `set-up`, and `up`. Feature staging
and build staging use a unique private directory inside this folder. Explicit
`--work-dir` selects the build staging location instead. Existing contents of
these folders are preserved. Feature configuration results retain their staging
paths; temporary metadata staging is removed after use.

Compose `up` also writes inline configs and environment-backed configs/secrets
in a private directory inside `--user-data-folder`. Without this option it uses
`$TMPDIR/dcb-compose`. File-backed sources are mounted directly and preserved.
An unmounted Docker volume records each private directory's ownership, without
secret values. `down` discovers these records without the creation-time folder
option or source configuration, removes the containers and their materialized
files, then removes the records. Cleanup failures return an error and retain
the record for a later `down` retry. Successful recreation removes previous
materialized roots once no container mounts them; a reused service or another
container can keep an older root. An interrupted or failed `up` can leave
materialized files; run `down` for that workspace to remove them.

`--container-data-folder` selects lifecycle and dotfiles marker storage on
`up`, `set-up`, `run-user-commands`, and `exec`. Relative paths resolve from the
container user's home. The default is `.devcontainer` under that home.
`exec` writes no lifecycle markers. Lifecycle hook markers retain upstream's
write-before-hook behavior; a failed hook remains marked for that container
creation or start. Dotfiles completion markers are written after a successful
install.

`--container-system-data-folder` is accepted on those same commands. Its
upstream default is `/var/devcontainer`. On `up` and `set-up`, dcb appends the
container environment to
`/etc/environment`, then preserves the container PATH in `/etc/profile`. These
root edits run once per container, with markers in this folder, including when
`--skip-post-create` is set. That flag skips the user environment probe.

`--container-session-data-folder` is accepted on `up`, `set-up`, and
`run-user-commands`, with no default. When supplied, dcb stores the probed
environment in `env-<probe-mode>.json` inside this container folder and reuses it
on later calls with the same folder and mode. Read, parse, or write failures are
non-fatal. No cache is read or written when the probe mode is `none` or lifecycle
commands are skipped. Remote environment overrides and lifecycle secrets are not
added to the cached probe result.

## Terminal dimensions

`up`, `set-up`, `run-user-commands`, `read-configuration`, and `exec` accept
`--terminal-columns` and `--terminal-rows`. Both are numbers with no explicit
size by default. Supply both flags together; a missing partner is a usage
error (exit 2), including when the supplied value is zero. If either value is
zero, the explicit size is not used.

Lifecycle commands use the supplied size for their pseudo-terminals in both
single-container and Compose mode, including named parallel commands. Internal
environment and marker probes keep their ordinary streams. `read-configuration`
accepts the same output settings without changing its configuration result.
For `exec`, dimensions size an allocated terminal; use `--tty` to allocate it.
Piped exec output retains separate stdout and stderr.
Initial PTY sizing needs Engine API 1.42 or later; the Docker SDK omits explicit
sizes when it negotiates an older API.

```sh
dcb up --workspace-folder . --terminal-columns 100 --terminal-rows 40
dcb set-up --container-id sample --workspace-folder . \
  --terminal-columns 100 --terminal-rows 40
dcb run-user-commands --workspace-folder . \
  --terminal-columns 100 --terminal-rows 40
dcb read-configuration --workspace-folder . \
  --terminal-columns 100 --terminal-rows 40
dcb exec --workspace-folder . --tty \
  --terminal-columns 100 --terminal-rows 40 -- sh
```

## Workspace mounts and container selection

`dcb up` bind-mounts the workspace into a single container. These flags change
the mount, add mounts, and select the container. Compose configurations mount
what their Compose files declare; `--mount` and `--id-label` also apply to the
primary Compose service.

| Flag | Commands | Default | Meaning |
| --- | --- | --- | --- |
| `--mount-workspace-git-root` | `up`, `exec`, `read-configuration`, `run-user-commands` | `true` | Mount the top folder of the workspace's Git working tree at `/workspaces/<name>`. The workspace folder is its subfolder there, for example `/workspaces/repository/app`. Outside a Git working tree, the workspace folder is mounted. `--mount-workspace-git-root=false` mounts only the workspace folder. |
| `--mount-git-worktree-common-dir` | `up`, `exec`, `read-configuration`, `run-user-commands` | `false` | For a linked worktree added with `git worktree add --relative-paths`, also mount the repository's common Git folder, so Git works in the container. Applies only with Git-root mounting. |
| `--workspace-mount-consistency` | `up` | `cached` | `consistent`, `cached`, or `delegated`. On macOS and Windows hosts the workspace mount gets this consistency; Linux hosts ignore it, as upstream does. |
| `--mount` | `up` | none | An additional mount, `type=<bind\|volume>,source=<source>,target=<target>[,external=<true\|false>]`. Repeat the flag for more mounts. `external=true` marks a Compose named volume as external. |
| `--id-label` | `up`, `exec`, `read-configuration`, `run-user-commands`, `down`, `inspect` | none | A `name=value` label. Repeat the flag for more labels. |

The Git root is found from the files in the workspace's parent folders; `dcb`
does not run `git`. `GIT_CEILING_DIRECTORIES` stops parent discovery before
a listed ancestor. Entries must be absolute paths. Git resolves symlinks in
entries until an empty path-list entry disables that resolution. Entries after
that marker retain literal path components. A ceiling equal
to the starting folder does not stop discovery. After a repository is found,
`GIT_WORK_TREE` selects its working tree. Relative values start at the workspace
folder; Git resolves symlinks and path components. An empty value or an invalid
intermediate component gives no Git root. For an outside-tree override, trailing
whitespace is removed from the resolved path as in the upstream CLI probe.
An explicitly empty `GIT_DIR`, or one that names a missing repository or an
invalid gitfile target, gives no Git root; parent discovery does not replace it.
Relative values start at the workspace folder. A valid `GIT_DIR` selects the
workspace folder without parent discovery; `GIT_WORK_TREE` can select its working
tree. Repository config, including `core.worktree`, is not interpreted. Other Git environment overrides
and ownership checks are not applied. `exec`, `run-user-commands`, `set-up`,
and an `up` that
reuses a container use the folder where that container mounts the workspace,
read from its bind mounts, whatever flags created it. A configured
`workspaceFolder` still wins. When the container mounts no folder that holds
the workspace, these commands calculate the folder from the flags, as
`read-configuration` does. `read-configuration`
returns the folder and mounts in its `workspace` object. A configuration that
sets `workspaceMount` keeps its mount. An explicitly empty `"workspaceMount": ""`
disables the default workspace mount; additional `mounts` and `--mount` values
still apply. `read-configuration` keeps the empty `workspaceMount` value. A
configuration that sets
`workspaceFolder` without `workspaceMount` keeps that folder and mounts the
workspace or its Git root at `/workspaces/<name>`, according to the Git-root
and worktree flags. Set `workspaceMount` to mount at a specific target.

Apart from the workspace mount, `dcb` mounts no host path that is not in the
configuration or in a flag. The default Git-root mount exposes the Git root,
which can hold more than the workspace folder; use
`--mount-workspace-git-root=false` to keep the mount to the workspace folder.
A relative `--mount` bind source resolves against the current directory, or
against the Compose project folder for a Compose configuration.

### Id labels

`--id-label` labels also derive `${devcontainerId}` in place of the workspace
labels `devcontainer.local_folder` and `devcontainer.config_file`. How they
select a container depends on the configuration mode.

For a single-container configuration, the id labels select the container in
place of the workspace labels. A container qualifies only if it carries every
given label.

- `up` reuses a container with every label, or creates one with exactly these
  labels and no workspace labels. `inspect --id-label` finds that container;
  `down --id-label` removes only a container that carries every given label.
  Both commands accept repeated labels and work without a configuration file.
- `up --remove-existing-container` removes only a container that has every
  label. A container that has only some of the labels is not removed.

For a Compose configuration, `up` selects and removes as it does without id
labels, as upstream does:

- `up` finds the primary container by its Compose project and service, and
  reuses it whatever its labels are. The id labels are set only when `up`
  creates the primary container; a changed id label does not recreate it.
- `up --remove-existing-container` and `down` with a loaded Compose configuration
  remove the workspace's Compose project, even with `--id-label`; id labels do
  not widen or narrow that removal.
  The project-conflict check still refuses a project that holds
  another workspace's containers.
- The id labels are added to the primary container beside the workspace labels,
  so the conflict check and workspace-scoped removal also cover it.

With id labels and no loaded configuration, `down` selects a container by every
label. If it is a Compose primary, `down` removes the project named by that
container's project label, using its workspace label for the project-conflict
check. The primary must carry a workspace ownership label; otherwise `down`
fails. The current folder never supplies the project to remove.

In both modes, `inspect`, `exec`, `read-configuration`, and
`run-user-commands` select the container that carries every given label.
When these commands or `down` use `--id-label` without `--workspace-folder`,
`--config`, or `--override-config`, they read no configuration. This follows
upstream's label-only selection. The configuration in the current directory is
not used.

## Feature controls

These upstream compatibility controls are hidden from command help. All default
to `false`.

| Option | Commands | Effect when enabled |
| --- | --- | --- |
| `--skip-feature-auto-mapping` | `build`, `exec`, `read-configuration`, `run-user-commands`, `up` | Leave legacy feature identifiers unchanged during resolution |
| `--skip-persisting-customizations-from-features` | `build` | Omit referenced feature customizations from new image metadata |
| `--omit-config-remote-env-from-metadata` | `up` | Omit the current configuration's remoteEnv from new metadata labels |
| `--omit-syntax-directive` | `build`, `up` | Omit generated Dockerfile syntax directives and ignore authored syntax directives during builds |

Legacy identifiers such as `node`, `golang`, and `common` map to upstream's
version 1 feature references by default. Explicit OCI references and local
feature paths stay unchanged. The skip flag disables this mapping wherever
features must be resolved; it does not change an existing container's features.
Unmapped bare legacy identifiers fail as unsupported, matching upstream.
The deprecated `gradle`, `maven`, and `jupyterlab` features are first folded
into Java or Python feature options, including when identifier mapping is skipped.

Configuration remoteEnv is persisted in metadata by default, matching upstream.
The omit flag affects only the current configuration's metadata contribution.
Runtime commands still receive configured remoteEnv values. Inherited metadata
stays intact. The customization control also preserves inherited and
configuration customizations, and features still install.

Syntax omission uses Docker's bundled Dockerfile frontend. It preserves the
build context, other parser directives, and the authored Dockerfile on disk.

```bash
dcb read-configuration --workspace-folder . --include-features-configuration --skip-feature-auto-mapping
dcb build --workspace-folder . --skip-persisting-customizations-from-features --omit-syntax-directive
dcb up --workspace-folder . --omit-config-remote-env-from-metadata
```

`up`, `set-up`, `run-user-commands`, and `exec` use
`--default-user-env-probe=loginInteractiveShell`. Select `none`,
`interactiveShell`, or `loginShell` to change the default. Configuration
`userEnvProbe` takes precedence. The probe reads the container user's shell
environment before applying remote environment values. If shell startup takes
more than 10 seconds, dcb warns and continues with no probed environment.

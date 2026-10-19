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
take `--container-id` to select a container directly.

## Results

Every command writes exactly one JSON object on one line to stdout, except
help and a valid `exec` invocation. An invalid invocation, including one of
`exec`, writes a JSON error result and exits with `2`. The `outcome` field is
`success` or `error`. An error result also has `message` and `description`:

<!-- rumdl-disable MD013 -->

```json
{"description":"dcb could not complete the requested command","message":"docker host: container not found","outcome":"error"}
```

<!-- rumdl-enable MD013 -->

Other result fields by command:

| Command | Fields |
| --- | --- |
| `up` | `containerId`, `remoteUser`, `remoteWorkspaceFolder`; `composeProjectName` for a Compose configuration |
| `inspect` | `container`, and the `up` fields |
| `run-user-commands`, `set-up` | `phasesRun`, and the `up` fields |
| `build` | `imageName`; `cacheOnly` or `outputDir` when the image is not loaded or pushed |
| `generate` | `dockerfile`, `contextDir` |
| `read-configuration` | `configuration`, `workspace`; `mergedConfiguration` with `--include-merged-configuration` |
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

Put `--` before the container command so that its flags are not read as
`dcb` flags:

```bash
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
the user, environment, and working directory for one command.

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
| `--output` | Exports the image filesystem into a local directory. It cannot be combined with `--push` or `--load`. |
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

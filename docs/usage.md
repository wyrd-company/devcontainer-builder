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
| `dcb features generate-docs` | Generates README files from local Feature metadata and notes. |
| `dcb features package <target>` | Packages a local Feature or collection as archives with collection metadata. |
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
- [Feature authoring commands other than `features package` and `features generate-docs`](https://github.com/wyrd-company/devcontainer-builder/issues/22)
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

### Package local Features

```sh
dcb features package ./src --output-folder ./packaged
dcb features package ./src/sample -o ./packaged -f
```

The target is required. A target with
`devcontainer-feature.json` is one Feature; otherwise its non-hidden child
folders with that file form a collection. Collection entries must have
`install.sh`. Each Feature must have `id`, `version`, and `name` metadata.
An ID that would put an archive outside the output folder is rejected.
Other metadata properties and source files are retained. Nonempty `legacyIds`
adds `currentId` with the Feature ID to the packaged metadata. Source files
are unchanged.

`--output-folder` (`-o`) defaults to `./output`. An existing output directory
causes an operation failure. `--force-clean-output-folder` (`-f`) removes it
before packaging. An error can leave archives from earlier collection entries;
the collection metadata is written only after all entries succeed.

The output contains `devcontainer-collection.json` and
`devcontainer-feature-<id>.tgz` for a single Feature, or
`devcontainer-feature-<folder>.tgz` for each collection entry. These are plain
tar archives with the upstream `.tgz` name. File bytes, modes, and symlinks are
preserved. `dcb` returns its usual JSON result with a `features` metadata array;
upstream logs progress without a stdout result. Packaging uses local files and
does not need Docker or a registry. See `TestPackageSingle`,
`TestPackageCollection`, and `TestFeaturesPackageCommand` for the contract checks.

### Generate Feature documentation

```sh
dcb features generate-docs --project-folder ./src --namespace example/collection
```

`--project-folder` (`-p`, default `.`) contains the Feature child folders;
the command does not append `src` or `test`. Hidden entries are skipped.
`--namespace` (`-n`) is required, and `--registry` (`-r`) defaults to `ghcr.io`.
A string flag without a value uses its declared default: an empty namespace or
GitHub owner/repository, `.` for the project folder, and `ghcr.io` for the registry.
An explicit empty string stays empty.
Grouped short flags use the upstream defaults for earlier flags in the group.
The camel-case aliases `--projectFolder`, `--githubOwner`, `--githubRepo`, and
`--logLevel` select the same values as their hyphenated names.
The example reference uses the Feature ID and the major part of its version,
or `latest` when the version is absent. Supply both `--github-owner` and
`--github-repo` to link metadata to that repository's `main` branch.

Each README includes the name, description, options in metadata order,
VS Code extensions, deprecation and legacy-ID notices, and optional `NOTES.md`.
Numeric option keys follow JavaScript key order. An empty option default is
`-`; false and zero remain values, and an absent default renders `undefined`,
as upstream. Markdown and replacement-string dollar patterns are retained with
upstream's JavaScript replacement semantics. Metadata uses the upstream
tolerant JSONC recovery behavior. Metadata and notes use Node-compatible UTF-8
replacement for malformed byte sequences. Object values retain JavaScript own
property and prototype conversion, including operation errors when conversion
fails. A list's length property controls whether its map operation is attempted;
invalid map operations also fail before replacing that entry's README.
String-valued options enumerate UTF-16 indices, as upstream.
Distinct escaped UTF-16 property keys remain distinct; isolated surrogate units
become replacement characters when the README is written.
Equivalent recovered UTF-16 keys select the same property. Repeated dotted
option assignments update one object until ordinary repeated values form an array.

Existing READMEs are replaced. An existing README symlink is unlinked; a dangling
symlink is followed, as upstream. Source metadata and notes stay unchanged.
Missing metadata, unreadable metadata, or a missing ID logs a diagnostic and
skips that entry with exit `0`. A folder, notes, or README write failure exits
`1`; other entries can already have completed. There is no collection rollback.
The command uses local files without Docker or registry access.

Progress and skip diagnostics use stderr. `dcb` retains its JSON result on stdout
and invocation-error exit `2`; upstream emits no stdout result and uses exit `1`
for invocation errors. `--log-level` accepts the shared dcb levels, including
upstream's `info`, `debug`, and `trace`, and `--log-format` remains available.
Repeated publication strings are comma-joined as upstream; repeated project
folders fail. See `TestGenerateDocumentationPinnedReadmes`,
`TestGenerateDocumentationFilesystem`, `TestGenerateDocumentationConversionReadmes`,
`TestFeaturesDocumentationCommand`, and `TestFeaturesDocumentationOptionalStringValues`.

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
| `--git-prefix` | `up`, `exec`, `read-configuration`, `run-user-commands` | none | The prefix of the host Git's build: the folder that Git puts in place of `%(prefix)` in an `include.path`. Without it, such an include followed by a relative path is not read. A relative value starts at the current folder. The upstream CLI has no such flag; it runs `git`, which knows its prefix. |
| `--workspace-mount-consistency` | `up` | `cached` | `consistent`, `cached`, or `delegated`. On macOS and Windows hosts the workspace mount gets this consistency; Linux hosts ignore it, as upstream does. |
| `--mount` | `up` | none | An additional mount, `type=<bind\|volume>,source=<source>,target=<target>[,external=<true\|false>]`. Repeat the flag for more mounts. `external=true` marks a Compose named volume as external. |
| `--id-label` | `up`, `exec`, `read-configuration`, `run-user-commands`, `down`, `inspect` | none | A `name=value` label. Repeat the flag for more labels. |

The Git root is found from the files in the workspace's parent folders and
Git's system and global config files; `dcb` does not run `git`. It reads these
files with the credentials of its own process; see
[Process credentials](/docs/dcb/overview#process-credentials).
Discovery follows the latest stable Git release, except where this section
names another Git release. `dcb` does not check the version of the installed
`git`; the upstream CLI runs it, so an older Git can mount a different folder.
As Git does when it starts, `dcb` first reads the system config file and then
the global config files. The system file is the file that `GIT_CONFIG_SYSTEM`
names, unless `GIT_CONFIG_NOSYSTEM` is true; a `GIT_CONFIG_NOSYSTEM` value
that Git does not read as a boolean gives no Git root. As Git does, `dcb`
removes `.` components and repeated separators from `GIT_CONFIG_SYSTEM`, and
removes each `..` component with the component before it, without reading the
file system. When a `..` component has no component before it, the path keeps
the changes made until then. Git 2.32 opens `GIT_CONFIG_SYSTEM` as given, so
`link/../system.cfg` names a file beside the folder that `link` points to, and
the upstream CLI with that Git can mount a different folder.
Without `GIT_CONFIG_SYSTEM`, Git reads the system
file that its build names, such as `/etc/gitconfig`; `dcb` does not read that
file. The global config files are the file that `GIT_CONFIG_GLOBAL` names, or
else `$XDG_CONFIG_HOME/git/config`, or `$HOME/.config/git/config` when
`XDG_CONFIG_HOME` is empty or unset, and then `$HOME/.gitconfig`. Relative
paths start at the workspace folder. A config file that Git rejects gives no
Git root: a syntax error, or an access failure other than a missing file or
denied permission. Git reads the files again after setup, from its working
folder: the working tree, after symbolic links are resolved, when the
workspace folder is inside it, and otherwise the workspace folder. Relative
paths then start at that folder. A folder or another file that Git cannot open
then also gives no Git root, and so does a system file that the process cannot
read because permission is denied. Each read takes a special file, such as a
terminal or a FIFO, until it ends, and a read error ends the file, as in Git.
As in Git, a FIFO read waits, with no time limit, until a writer opens the
FIFO, and the file ends when the last writer closes it. Both reads follow
`include.path` entries when Git reads them, as Git does. A relative include
path starts at the including file's folder, `~/` starts at `HOME`, and `~name/`
at the home folder of that account. Git asks every account service that the
host names, such as `/etc/passwd`, LDAP or systemd. The Linux release builds
look up accounts in `/etc/passwd` only. They skip a `~name/` include whose
account is not there, as if the included file were missing. Git reads that
file for an account that another service knows, and rejects a name that no
service knows. When Git rejects the name or that file, the upstream CLI mounts
the workspace folder, and `dcb` mounts the Git root. This is an approved
difference; see
[Relation to the Dev Container CLI](/docs/dcb/overview#relation-to-the-dev-container-cli).
Builds with cgo and macOS builds ask the account services, as Git does. A
`%(prefix)/` path followed by an absolute path names that path, as in Git.
Git reads `%(prefix)/` followed by a relative path at the prefix of its build,
a separator and the rest. That prefix is the folder that Git was built to be
installed in, usually the folder above the `bin` folder of the `git` command,
such as `/usr` for `/usr/bin/git`; Homebrew builds name their `opt` folder,
such as `/opt/homebrew/opt/git`. `dcb` does not run `git`, so `--git-prefix`
names that prefix, and `dcb` then reads that path, as Git does. A relative
`--git-prefix` starts at the current folder of the command. A relative
`Options.GitPrefix` of the library gives a relative path, which starts at the
including file's folder. A command-line include has no including file, so
a relative expanded path is rejected.
Without `--git-prefix`, `dcb` does not read such an include, as if the
included file were missing. When Git rejects that file, the upstream CLI
mounts the workspace folder, and `dcb` mounts the Git root. This is an
approved difference; see
[Relation to the Dev Container CLI](/docs/dcb/overview#relation-to-the-dev-container-cli).
A missing included file is skipped.
An include without a value, a `~` path that cannot be expanded, an access
failure other than a missing file, a file that Git cannot open, such as a
folder, a syntax error, and more than 10 nested includes give no Git root. As
in Git, a relative config file name, and a relative include in that file, is
looked up from the folder that the read starts at, so the length of that
folder's path does not limit it, and the process needs only search permission
on that folder, as Git's `chdir` does. On macOS, a thread of its own changes
to that folder to look the name up. On hosts other than Linux and macOS, `dcb`
joins the name to the folder's path instead. The read after setup then also
reads the repository config files of the format check, described below, with
their includes. Last, it reads command-line config from the environment:
`GIT_CONFIG_COUNT` pairs named `GIT_CONFIG_KEY_0` and `GIT_CONFIG_VALUE_0`,
and the following indices, then `GIT_CONFIG_PARAMETERS`. A count, missing
pair, key or parameter quoting that Git rejects gives no Git root.
`GIT_CONFIG_PARAMETERS` accepts both `'key=value'` and `'key'='value'` forms.
These entries also follow `include.path`, with the account and prefix rules
above. The expanded path must be absolute: a relative include has no file
whose folder can be its base, and gives no Git root. A relative include
inside that file starts at that file's folder. A missing absolute include
is skipped. After the full read finishes, any `core.ignorecase`, `core.filemode`,
`core.symlinks`, `core.trustctime`, `core.quotepath`, `core.ignorestat`,
`core.sparseCheckout`, `core.sparseCheckoutCone`, `core.protectHFS`,
`core.protectNTFS`, `core.precomposeUnicode`, `core.fsyncObjectFiles`,
`core.safecrlf`, `core.autocrlf`, `core.lockfilePid`,
`sparse.expectFilesOutsideOfPatterns`, `pager.color`, `color.pager`,
`user.useConfigOnly`, `advice.detachedHead` or `advice.statusHints` value that
Git cannot read as a boolean
gives no Git root, even if a later value is valid. `core.safecrlf`
also accepts `warn`, without regard to case. `core.autocrlf` also accepts `input`,
without regard to case.
Native discovery follows current Git for `core.lockfilePid`; Git before 2.54
ignores that key.
A key without a value is true; an empty value is false. Boolean words and
integers follow Git's boolean rules. The initial read and repository-format
read do not apply these keys' types. Other typed values from the full config
read and `includeIf` conditions are not applied.
As Git does, discovery walks the physical parent folders, after symbolic links
in the workspace folder's path are resolved. The Git root keeps the workspace
folder's path, up as many folders as Git walked, as the upstream CLI resolves
Git's relative result.
`GIT_CEILING_DIRECTORIES` stops parent discovery before
a listed ancestor. Entries must be absolute paths. Git resolves symlinks in
entries until an empty path-list entry disables that resolution. Entries after
that marker retain literal path components. A ceiling equal
to the starting folder does not stop discovery. Parent discovery also stops at
the first physical parent folder on another file system, unless
`GIT_DISCOVERY_ACROSS_FILESYSTEM` is true. Git compares device numbers, so a
bind mount of the same file system is not a boundary. A value that Git does not
read as a boolean gives no Git root. After a repository is found,
`GIT_WORK_TREE` selects its working tree. Relative values start at the workspace
folder; Git resolves symlinks and path components. An empty value or an invalid
intermediate component gives no Git root. For an outside-tree override, trailing
whitespace is removed from the resolved path as in the upstream CLI probe.
An explicitly empty `GIT_DIR`, or one that names a missing repository or an
invalid gitfile target, gives no Git root; parent discovery does not replace it.
As in Git 2.54 and later, when `stat` fails on a `.git` entry, parent discovery
continues only if the entry is missing, which includes a symbolic link to a
missing path or through a regular file. Any other `stat` failure gives no Git
root: for example a symbolic link loop, a name that is too long, or a link into
a folder that cannot be searched. A `.git` entry that is not a folder must be a
gitfile: a gitfile that cannot be opened or read, or a special file such as a
FIFO, also gives no Git root. A `.git` folder is tested as a repository instead,
and a folder that fails that test, such as one that cannot be read, does not
stop parent discovery.
Gitfiles must start with literal `gitdir:` followed by one space and a repository
path. Only final CR and LF bytes are removed; other whitespace and interior
newlines belong to the path. Gitfiles larger than 1 MiB give no Git root.
A relative gitfile target starts at the gitfile's folder and keeps its path
components, as Git does: a symbolic link is resolved before a later `..`, and
a regular file or a missing folder before `..` gives no Git root.
Repository admission checks the first 255 bytes of `HEAD` for a `ref:` target
starting with `refs/` or a 40-digit hexadecimal object ID. A symbolic-link
`HEAD` must point to a path starting with `refs/`; its target need not exist.
A `commondir` pointer selects the common folder whose `objects` and `refs`
paths must pass Git's executable-access check. Relative pointers start at the
administrative folder; final CR and LF bytes are removed. A failed pointer read
or path resolution ends discovery. A resolved folder without those signatures
is not a repository; parent discovery can continue past a `.git` directory.
After the `.git` entry, each folder is also tested as a repository, as Git does.
When the workspace is in a bare repository or in a `.git` folder, Git reports no
working tree, and the workspace folder is the Git root. The global
`safe.bareRepository` setting is not read.
`GIT_OBJECT_DIRECTORY` replaces the `objects` signature path during admission.
Relative values start at the workspace folder and retain physical path traversal.
An empty or inaccessible value fails admission; `refs` is still checked in the
common folder. `GIT_COMMON_DIR` replaces the `commondir` pointer, which is then
not read. Relative values start at the workspace folder and retain physical
path traversal; an empty value checks `/objects` and `/refs`, as Git does.
Git keeps the value as given: its repository format check starts a relative
value at the folder where it found the repository, or at the workspace folder
for `GIT_DIR`, and its config read after setup at its working folder.
The common-folder mount option reads pointer lines separately,
as upstream does.
Git's repository format check then reads the common folder's `config`, and
`config.worktree` when `extensions.worktreeConfig` is set. Config that Git
rejects gives no Git root: a syntax error, a directory or an unreadable file,
a `core.repositoryformatversion` that is not an integer or is above 1, an
invalid `core.bare`, `extensions.worktreeConfig` or `extensions.preciousObjects`
boolean, and `core.worktree` or `extensions.partialClone` without a value. A
special file, such as a FIFO or a device, is read as a stream. A FIFO waits
for a writer with no time limit. The format check stops at the first syntax
or typed value error, or at the end of the stream; a read error also ends the
stream.
The format check does not follow includes. The full read after setup opens
these files again and follows their includes.
Extensions follow Git 2.55 built without Rust. Git checks each extension value
whatever the repository version: `extensions.objectFormat` and
`extensions.compatObjectFormat` must be `sha1` or `sha256`,
`extensions.refStorage` must name `files` or `reftable`, optionally followed by
`://` and a payload, and `extensions.relativeWorktrees` and
`extensions.submodulePathConfig` must be booleans. A second
`extensions.compatObjectFormat` gives no Git root. When
`core.repositoryformatversion` is 1, an extension that Git does not know gives
no Git root, and so does any `extensions.compatObjectFormat`. When it is 0,
`extensions.noop-v1` or one of these version 1 extensions gives no Git root;
other unknown extensions are ignored. `dcb` applies these rules whatever Git is
installed. The upstream CLI runs the installed `git`, and other Git versions and
builds accept other repositories, so the two can mount different folders: Git
before 2.54 does not know `extensions.submodulePathConfig` or the `://` payload
form, Git before 2.48 `extensions.relativeWorktrees`, Git before 2.45
`extensions.compatObjectFormat` and Git before 2.44 `extensions.refStorage`.
Git 2.45 to 2.53, and Git built with Rust, accept a compatibility object format
that differs from the object format. Integers
are read as Git built against a C23 C library, such as glibc 2.38 or later,
reads them: decimal, octal with a leading `0`, hexadecimal with `0x` and binary
with `0b`, with an optional `k`, `m` or `g` unit. When
`core.repositoryformatversion` is 0 or 1, a true `core.bare` leaves a discovered
repository without a working tree, and the workspace folder is the Git root. A
linked worktree and `GIT_COMMON_DIR` ignore `core.bare` in the common config.
Otherwise `core.worktree` selects the working tree, with the same version and
common-config rules; `GIT_WORK_TREE` and a true `core.bare` take precedence. An
absolute value is resolved as `GIT_WORK_TREE` is. A relative value starts at
the repository folder and must name an existing folder that the effective user
and capabilities can search, as Git's `chdir` requires; its symbolic links are
resolved before `..` components. Git changes to the
repository folder first, so the folder and the value are separate lookups, each
with its own symbolic link and path length limits. A `.git` folder above the
workspace folder and a gitfile target first pass through Git's own path
resolution, which allows 33 symbolic links. On Linux, the selected folder is
named from `/proc`, or without it by a thread that unshares its working
folder. Where a seccomp profile also blocks `unshare`, as Docker's default
profile does, both lookups are repeated one component at a time, so the value
can pass a folder whose physical path exceeds `PATH_MAX`; the selected folder's
own path must fit in `PATH_MAX`, as Git's working folder must.
Builds for hosts other than Linux and macOS instead join the repository
folder's physical path and the value, which must fit the host's path length
limit. An empty value or one that Git cannot resolve gives no Git
root. Inside that working tree, the Git root keeps the workspace folder's path;
outside it, the Git root is the working tree's physical path. The format check
does not follow includes, and other config keys are not interpreted.
Relative values start at the workspace folder. A valid `GIT_DIR` selects the
workspace folder without parent discovery; `GIT_WORK_TREE` or the repository
config can select its working tree. Other Git
environment overrides
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

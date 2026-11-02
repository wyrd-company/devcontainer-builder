---
name: dcb
description: >-
  Build and manage development containers with dcb, inspect configuration,
  run container commands and lifecycle hooks, or stage features and
  VS Code server archives.
relationships:
  references: [CONTRIBUTING]
---

# Development containers with dcb

Use `dcb --help` to read command descriptions, common flags, and examples.
Help is plain text on stdout. Use `dcb <command> --help`
for command flags. Use `--workspace-folder` for the local workspace and
`--config` for an explicit devcontainer.json. `--override-config` supplies
alternate content while relative paths stay based on `--config` (or the
workspace configuration path). For commands other than `exec`, flags can
follow positional arguments.

Use upstream configuration forms: `build.dockerfile` with `build.context`, or
`dockerFile` with top-level `context`. Both paths resolve from
`devcontainer.json`.
`dockerComposeFile` and `build.cacheFrom` accept strings or string arrays;
parallel lifecycle command values are strings or string arrays.

Common-only configurations can be read; build and up require a source.
An empty `dockerComposeFile` array discovers files from workspace
`COMPOSE_FILE`,
then workspace `.env`, then `docker-compose.yml` and its existing override file.
`containerEnv` values are strings; `remoteEnv` values are strings or null.
Remote null becomes the literal string `"null"` for exec and lifecycle commands.

Kubernetes library image-source projection uses the upstream command wrapper and
Feature entrypoints. With `overrideCommand: false`, supply native OCI
ImageConfig
in the projection or host request; missing image configuration is an error.
Configuration environment overrides Features; the image supplies inherited
values.
This is a library surface; remaining Kubernetes argument translations need
checks.

`exec` stops reading options at the container command; every later argument
passes through unchanged. An explicit `--` before the command also works.

Invocations except help and exec normally emit one JSON object on one stdout
line.
`outdated` emits its version report instead.
`upgrade` writes no stdout on success, or pretty lockfile JSON with `--dry-run`.
It retains existing integrity pins. The paired `--feature`/`--target-version`
flags update a requirement before resolution, including during dry-run.
Dry-run can refresh an existing lockfile. Operation errors use stderr and exit
1.
`features resolve-dependencies` emits a Mermaid graph followed by pretty JSON
installation order. It reads `.devcontainer.json` before
`.devcontainer/devcontainer.json`, uses lockfile pins, and does not install
Features or write lockfiles. Cycles fail after graph output.
`features info <mode> <feature>` emits published Feature information as text
or JSON (`--output-format json`). Modes are manifest, tags, dependencies, and
verbose. Every mode requires a manifest; JSON dependencies returns `{}`.
Archive exports to stdout emit only archive bytes. An interactive terminal is
refused with exit code 2 and a JSON error result; use a pipe or file
destination.
Check `outcome`: `success` or `error`. Errors include `message` and
`description`. Invalid invocations exit 2 and retain this JSON error result,
including invalid exec invocations. Other operation failures exit 1.

`exec` passes the child's stdout and stderr through unchanged in text and JSON
log modes, emits no result line, and returns the child's exit code. Capture
stdout to capture command output. With `--tty`, the terminal combines child
streams on stdout. If dcb fails before or outside the child, stdout is empty,
stderr carries a dcb error, and dcb exits 1. A child exit code of 1 is
ambiguous;
dcb marks its text-mode error diagnostic on stderr with `dcb: error: `.
In JSON mode, the error record has `type: diagnostic`, `level: error`, and the
message in `text` without the text-mode prefix. These identify dcb diagnostics;
a child can also print the same marker or record.

Diagnostics and other build/lifecycle output go to stderr. `--log-format json`
encodes dcb diagnostics as `type: diagnostic` with `level` and `text` fields.
Child exec output remains raw. Build/lifecycle output uses `type: output` with
`text`, independently of `--log-level`. Text mode writes that output unchanged.
`--log-level` filters diagnostics at trace, debug, info, warn, error, or off.
Warnings show at warn; error hides warnings; off hides optional diagnostics.
Exec operation errors still show at off. Debug adds command start diagnostics;
trace also includes the command arguments.

Start and inspect a container:

```sh
dcb read-configuration --workspace-folder .
dcb up --workspace-folder .
dcb exec --workspace-folder . sh -c 'id && pwd'
dcb exec --workspace-folder . -- sh -c 'id && pwd'
dcb inspect --workspace-folder .
dcb down --workspace-folder .
```

For `exec`, configuration `remoteEnv` wins over a `--remote-env KEY=VALUE`
with the same name, as upstream. Values set only by the flag are added.

`up` returns `containerId`, `remoteUser`, and `remoteWorkspaceFolder`.
Image, Dockerfile, and Compose configurations select the library host
without invoking another container CLI. Docker connection settings use the
standard `DOCKER_*` environment. Builds use Docker Engine's BuildKit by
default; `--buildkit-addr` selects a separate BuildKit instance.

Numeric `appPort` values map to the same port on loopback. Strings retain Docker
publishing syntax; `"8080"` requests an allocated host port. Docker mount
strings
in `workspaceMount`, `mounts`, and `runArgs --mount` retain Docker options and
CSV
quoting. An omitted string mount type defaults to volume. Invalid options fail.

Build or inspect build artifacts:

```sh
dcb build --workspace-folder . --image-name example:dev
dcb generate --workspace-folder . --work-dir ./generated
dcb features --workspace-folder . --out ./staged-features
dcb features package ./src --output-folder ./packaged
dcb features generate-docs -p ./src -n example/collection
dcb features resolve-dependencies --workspace-folder .
dcb features info verbose ghcr.io/example/collection/sample:1
dcb templates generate-docs -p ./templates
dcb server --out ./server --platform linux/amd64
```

`features package <target>` packages local Features without Docker. The target
is required, and output defaults to `./output`. An existing
output directory is refused unless `-f` removes it first. Use `-o` to choose the
output folder. The result contains Feature metadata; archives and
`devcontainer-collection.json` stay in the output folder.

`features generate-docs` writes README.md in visible Feature child folders of
`--project-folder` (`-p`, default `.`). `--namespace` (`-n`) is required;
`--registry` (`-r`) defaults to `ghcr.io`. Supply both `--github-owner` and
`--github-repo` for metadata links. It includes options, VS Code extensions,
NOTES.md, and deprecation/legacy-ID notices. Missing metadata or IDs are skipped
with diagnostics; other failures can leave partial results. It replaces existing
READMEs and returns the normal JSON result. It needs no Docker or registry.
String flags without values use their declared defaults; explicit empty strings
stay empty. Conversion/map errors preserve that entry's README while sibling
entries can complete.
Dotted publication properties convert only when used. A shadowed `toString`
method can fail the operation; empty collections, skipped entries, and unused
GitHub link values do not perform that conversion.

`build` loads into Docker by default and returns an `imageName` array.
Use `--push` for registry export, `--platform` for target platforms,
`--no-cache` to disable cache, and repeated `--cache-from` / `--cache-to`
for cache settings. Use `build --load=false` to warm the build cache without
exporting an image. `--output` accepts a buildx exporter specification or
directory
and cannot be
combined with `--push` or `--load`. The result contains `cacheOnly: true`
for a cache-only build, or `output` for an exporter (`outputDir` also names a
local
destination). Archive exports to stdout omit the JSON result. `generate` returns
`dockerfile` and `contextDir` paths and retains its output folder.
`build` and `up` remove staging on success and failure unless `--keep` is set,
including an explicit `--work-dir`. Build errors report `contextDir` only when
`--keep` retains the context.

`build`, `generate`, `up`, and `read-configuration` take
`--additional-features '<json>'`, an
object in the form of the `features` property. Its features follow the
configuration's; for a feature the configuration names, the configuration's
options win. Invalid JSON exits `2`.

`up` uses `--build-no-cache` to disable build cache. Export flags `--push`,
`--load`, and `--output`, and `--no-cache`, belong to `build`.
`up --update-remote-user-uid-default on` remaps unless the configuration sets
`updateRemoteUserUID: false`; on Linux, `off` remaps only with an explicit
`true`;
`never` disables remapping. Linux remaps by default. macOS requires explicit
`--update-remote-user-uid-default on`, even with `updateRemoteUserUID: true`.

`up --gpu-availability detect` checks the Engine NVIDIA runtime when
`hostRequirements.gpu` requests GPUs. `all` requests all GPUs without detection;
`none` skips automatic requests. Explicit `runArgs` GPU selection is preserved.
Compose adds a primary-service GPU device reservation under the same rules;
explicit service GPU reservations and `gpus` selection take precedence.

`initializeCommand` runs on the host on every `up`, before build/start, in the
local workspace with the host environment. Strings use the host shell, arrays
execute directly, and object entries run in parallel. Executable POSIX text
scripts without a shebang use `/bin/sh`. Failures abort startup;
SIGINT reports interruption and continues. It has no marker and does not run
on build, generate, read-configuration, set-up, exec or run-user-commands.
`up --skip-post-create` skips container lifecycle commands;
`--skip-post-attach` skips only postAttachCommand. Use
`run-user-commands` to run lifecycle commands in an existing devcontainer.
Use `set-up --container-id <id> --workspace-folder .` to apply the
configuration's lifecycle commands to an existing container. These commands
use lifecycle markers to avoid repeating completed creation/start hooks.
Their `phasesRun` result lists executed phases as objects with `hook` (the
upstream name, such as `postAttachCommand`) and `origin` (such as
`devcontainer.json` or the feature reference). No executed phases gives `[]`.

`up`, `run-user-commands`, and `set-up` accept
`--skip-non-blocking-commands` to stop after the merged `waitFor` hook
(default `updateContentCommand`), without background continuation.
`up` and `run-user-commands` accept `--prebuild` to stop after
`updateContentCommand` and rerun that hook despite a current marker.
`run-user-commands --stop-for-personalization` stops after dotfiles and
before `postStartCommand`. Combined controls use the first stop point.
`up --expect-existing-container` requires an existing running or stopped
container before build or creation. Explicit `--remove-existing-container`
then replaces it; otherwise Compose reuses it without recreation.
All four controls default to false.
Successful `run-user-commands` returns `result`: `skipNonBlocking`, `prebuild`,
`stopForPersonalization`, or `done` (default). This identifies the boundary even
when no command runs there or markers skip commands. At updateContent,
`skipNonBlocking` precedes `prebuild`; `prebuild` precedes personalization.
Failures return the error outcome without `result`.

`up`, `run-user-commands`, and `set-up` take `--dotfiles-repository`,
`--dotfiles-install-command`, and `--dotfiles-target-path` (default
`~/dotfiles`). The install runs after postCreateCommand, once per container,
as the remote user; the container needs `git`. A failed install still gives a
`success` result, with a `warn` diagnostic on stderr; the next `up` retries.

`up`, `run-user-commands`, and `set-up` take `--remote-env KEY=VALUE`
(repeatable); `up` and `run-user-commands` take `--secrets-file <path>`, a
JSON object of string, number or boolean values (numbers and booleans become
their JavaScript text). Both apply only to that command's lifecycle
commands and dotfiles install; `exec` does not get them. For these commands,
configuration `remoteEnv` wins over `--remote-env`; secrets win over both.
Secret values,
and their quoted, JSON, base64 and URL-escaped forms, show as `********` in
stderr
and the result. An unreadable or malformed
secrets file exits `1` and names the path, not the content.

`dcb version` and `dcb --version` return the version. `dcb skills --list`
lists bundled skills; `dcb skills dcb` prints this skill in the result's
`skills[].content`.
`dcb skills install --dir .agents/skills` writes `dcb/SKILL.md` below the
chosen directory.

`up` and `set-up` accept `--include-configuration` and
`--include-merged-configuration` to add configuration snapshots to their
results.
Both default to false. `read-configuration --include-features-configuration`
adds resolved feature metadata, values and sources as `featuresConfiguration`
when features exist. `read-configuration` substitutes the existing container's
environment in configuration, merged output and resolved feature options,
independently of the include flags. Without a container, container environment
references remain unresolved. Ordinary reads without an explicit container ID
also work when Docker is unreachable.

## Data folders

Use `--user-data-folder` on build, exec, read-configuration, run-user-commands,
set-up, and up to place private feature/build staging inside a selected host
folder. Explicit `--work-dir` overrides build staging. Existing contents stay
intact. Compose up also stores inline configs and environment-backed
configs/secrets there
(default: `$TMPDIR/dcb-compose`). Down discovers their private directories from
Docker ownership records without repeating the folder option or supplying the
source configuration. It removes materialized copies, preserves file-backed
sources, and reports cleanup errors for retry. Successful recreation removes old
roots once no container mounts them; reused services can keep a shared root.
Run down after a failed or
interrupted up to remove retained materialized copies.
Use `--container-data-folder` on up, set-up, run-user-commands, and exec
for lifecycle/dotfiles markers (default: `.devcontainer` under the container
user's home). Exec does not write markers. Use `--container-system-data-folder`
(default: `/var/devcontainer`) for the root environment and profile patch
markers on up and set-up. These patches run before lifecycle commands, including
under `--skip-post-create`; skipped lifecycle commands do not probe the user's
shell environment. Use `--container-session-data-folder` on up, set-up, and
run-user-commands to persist and reuse `env-<probe-mode>.json` in the container.
Cache failures are non-fatal. The default empty folder disables persistence.

### Terminal dimensions

`up`, `set-up`, `run-user-commands`, `read-configuration`, and `exec` accept
`--terminal-columns` and `--terminal-rows` together. Both are numeric; a zero
value leaves the explicit size unset. Lifecycle output uses the selected PTY
size. For `exec`, use `--tty` to allocate a terminal; dimensions alone preserve
piped output.

`build` and `up` accept the hidden booleans `--experimental-lockfile` and
`--experimental-frozen-lockfile` (both default false). The first creates a
feature lockfile; existing lockfiles update by default after success. Frozen
mode requires an unchanged lockfile and never writes. OCI features and their
dependencies resolve by the recorded integrity digest; HTTPS archives are
checked against their recorded digest. The file is `devcontainer-lock.json`
next to the configuration, or `.devcontainer-lock.json` for a dot-prefixed
configuration. Publication is atomic and follows successful command completion.
`read-configuration --include-features-configuration` uses the same pins.

Upstream feature controls are hidden and default to false.
`--skip-feature-auto-mapping` on `build`, `up`, `exec`, `read-configuration`,
and `run-user-commands` disables legacy feature identifier mapping.
`build --skip-persisting-customizations-from-features` omits referenced feature
customizations from image metadata. Configuration and inherited customizations
remain. `up --omit-config-remote-env-from-metadata` omits configuration
remoteEnv
from new metadata labels; remoteEnv persists by default and remains available to
runtime commands. `build --omit-syntax-directive` and
`up --omit-syntax-directive`
ignore authored syntax directives and omit generated directives.

`templates generate-docs` writes README.md in visible Template child folders
from
`devcontainer-template.json` and optional NOTES.md. It uses `-p` (default `.`),
`--github-owner`, and `--github-repo`; success leaves stdout empty.

## Report Feature versions

Use `dcb outdated --workspace-folder .` for a text table, or add
`--output-format json` for a `features` report with current, wanted,
wantedMajor,
latest, and latestMajor fields. Unavailable JSON fields are omitted. The report
has no outcome field. Config order is retained. Local Features are omitted.
Current uses the lockfile when present; wanted follows the configured tag or
digest metadata; latest uses the highest full semver tag. Lockfiles are
unchanged.
This command needs registry access and no Docker daemon. `--config` selects an
explicit file. User data folder is accepted and unused. Log levels are info,
debug, and trace. Terminal dimensions must be supplied together.

Docker `runArgs` accepts boolean assignments, combined short booleans, attached
short values, and bare environment keys. `--rm`, stdin and TTY settings reach
the Engine API. `--entrypoint` is replaced by the upstream keepalive entrypoint.

New Docker containers inherit stdout/stderr attachment. `--attach` validates
case-insensitive stdin/stdout/stderr; `-i` also attaches stdin. A final true
`--detach` conflicts with inherited attachment; `--detach=false` is valid.
`--cidfile` reserves a client-side path before creation and writes the ID
without
a newline before start. Existing files stop creation unchanged; failed creation
or writing removes the unwritten reservation. Written IDs persist. Repeated
paths use the final value; an empty final value disables the file.

Docker `runArgs --env-file` and `--label-file` use native Docker file parsing.
Files load before explicit environment and label flags. Paths use the process
working directory. Merged init, privileged, capability and security option flags
follow runArgs, as in the upstream CLI.

`--umask` uses Docker octal parsing and the final value, including explicit
zero.
Creation confirms the value in daemon inspection before start. If the daemon
omits or changes it, dcb removes the new container and reports an error.
`--kernel-memory` validates Docker memory-size syntax and has no effect, as in
the native Docker CLI's deprecated flag.

Docker CPU run arguments preserve exact NanoCPU values through native Docker
parsing.
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
`--health-retries`) retain Docker values. `--no-healthcheck` disables image
health
checks and conflicts with nonzero health options. Repeated scalar flags use the
last value. `--log-driver` and `--log-opt` preserve logging configuration;
repeated
option keys use the last value. Docker rejects logging options for driver
`none`.

`build.options --network` supports default, none and host for Dockerfile RUN
instructions. Repeated values use the last value. Host mode requests the native
BuildKit network.host entitlement. The daemon must allow it. Build flags other
than network, image labels, build arguments and target selection
currently return an explicit unsupported-option error.

`build.options --label` sets image labels through native BuildKit. Repeated
keys use the last value; bare keys use empty values. Empty keys fail before
staging. Explicit library labels override configuration labels.

Scratch-only Dockerfiles can build without filesystem layers. Image
configuration
is retained when the Dockerfile frontend returns an empty filesystem.

Build options accept repeated `--build-arg KEY=VALUE` and
`--build-arg=KEY=VALUE`.
Arguments override `build.args`; explicit library build arguments apply last.
Bare keys inherit the host environment when set. Unset bare keys leave earlier
values intact; explicit empty values remain empty. Empty keys are invalid.
Metadata and image user inference use `build.args` independently of these
options,
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
uses the process environment); explicit empty maps disable inheritance. Final
unset
keys require `ImageConfig`; removing an image-owned variable returns an error.
Merged security flags follow `runArgs`; merged security options use first-seen
union order. Detach conflicts with inherited output
attachment, and enabled init has no PodSpec equivalent. Other Docker flag and
remaining Compose translations remain separate coverage.

Kubernetes `ProjectCompose` primary command projection defaults
`overrideCommand`
to false. Its shell wrapper runs Feature entrypoints in order, then the service
entrypoint and command. Absent service values fall back to the image; an
explicit
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

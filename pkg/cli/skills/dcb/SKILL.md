---
name: dcb
description: Build and manage development containers with dcb, inspect configuration, run container commands and lifecycle hooks, or stage features and VS Code server archives.
relationships:
  references: [CONTRIBUTING]
---
# Development containers with dcb

Use `dcb --help` to read command descriptions, common flags, and examples.
Help is plain text on stdout. Use `dcb <command> --help`
for command flags. Use `--workspace-folder` for the local workspace and
`--config` for an explicit devcontainer.json. `--override-config` supplies
alternate content while relative paths stay based on `--config` (or the
workspace configuration path). Flags can follow positional
arguments. Put `--` before the container command's own flags.

Invocations except help and exec normally emit one JSON object on one stdout line.
Archive exports to stdout emit only archive bytes.
Check `outcome`: `success` or `error`. Errors include `message` and
`description`. Invalid invocations exit 2 and retain this JSON error result,
including invalid exec invocations. Other operation failures exit 1.

`exec` passes the child's stdout and stderr through unchanged in text and JSON
log modes, emits no result line, and returns the child's exit code. Capture
stdout to capture command output. With `--tty`, the terminal combines child
streams on stdout. If dcb fails before or outside the child, stdout is empty,
stderr carries a dcb error, and dcb exits 1. A child exit code of 1 is ambiguous;
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
dcb exec --workspace-folder . -- sh -c 'id && pwd'
dcb inspect --workspace-folder .
dcb down --workspace-folder .
```

`up` returns `containerId`, `remoteUser`, and `remoteWorkspaceFolder`.
Image, Dockerfile, and Compose configurations select the library host
without invoking another container CLI. Docker connection settings use the
standard `DOCKER_*` environment. Builds use Docker Engine's BuildKit by
default; `--buildkit-addr` selects a separate BuildKit instance.

Build or inspect build artifacts:

```sh
dcb build --workspace-folder . --image-name example:dev
dcb generate --workspace-folder . --work-dir ./generated
dcb features --workspace-folder . --out ./staged-features
dcb server --out ./server --platform linux/amd64
```

`build` loads into Docker by default and returns an `imageName` array.
Use `--push` for registry export, `--platform` for target platforms,
`--no-cache` to disable cache, and repeated `--cache-from` / `--cache-to`
for cache settings. Use `build --load=false` to warm the build cache without
exporting an image. `--output` accepts a buildx exporter specification or directory
and cannot be
combined with `--push` or `--load`. The result contains `cacheOnly: true`
for a cache-only build, or `output` for an exporter (`outputDir` also names a local
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
`updateRemoteUserUID: false`; on Linux, `off` remaps only with an explicit `true`;
`never` disables remapping. Linux remaps by default. macOS requires explicit
`--update-remote-user-uid-default on`, even with `updateRemoteUserUID: true`.

`up --gpu-availability detect` checks the Engine NVIDIA runtime when
`hostRequirements.gpu` requests GPUs. `all` requests all GPUs without detection;
`none` skips automatic requests. Explicit `runArgs` GPU selection is preserved.
Compose adds a primary-service GPU device reservation under the same rules;
explicit service GPU reservations and `gpus` selection take precedence.

`up --skip-post-create` skips all lifecycle commands;
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
configuration `remoteEnv` wins over `--remote-env`; secrets win over both. Secret values,
and their quoted, JSON, base64 and URL-escaped forms, show as `********` in stderr
and the result. An unreadable or malformed
secrets file exits `1` and names the path, not the content.

`dcb version` and `dcb --version` return the version. `dcb skills --list`
lists bundled skills; `dcb skills dcb` prints this skill in the result's `skills[].content`.
`dcb skills install --dir .agents/skills` writes `dcb/SKILL.md` below the
chosen directory.

`up` and `set-up` accept `--include-configuration` and
`--include-merged-configuration` to add configuration snapshots to their results.
Both default to false. `read-configuration --include-features-configuration`
adds resolved feature metadata, values and sources as `featuresConfiguration`
when features exist.

### Data folders

Use `--user-data-folder` on build, exec, read-configuration, run-user-commands,
set-up, and up to place private feature/build staging inside a selected host
folder. Explicit `--work-dir` overrides build staging. Existing contents stay
intact. Use `--container-data-folder` on up, set-up, run-user-commands, and exec
for lifecycle/dotfiles markers (default: `.devcontainer` under the container
user's home). Exec does not write markers. System and session folder flags are
accepted on their upstream commands; system patch markers and user environment
probe caches have no current writer.

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
remain. `up --omit-config-remote-env-from-metadata` omits configuration remoteEnv
from new metadata labels; remoteEnv persists by default and remains available to
runtime commands. `build --omit-syntax-directive` and `up --omit-syntax-directive`
ignore authored syntax directives and omit generated directives.

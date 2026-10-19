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

Invocations except help and exec emit one JSON object on one stdout line.
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
exporting an image. `--output` selects a filesystem export and cannot be
combined with `--push` or `--load`. The result contains `cacheOnly: true`
for a cache-only build, or `outputDir` for a filesystem export. `generate` returns `dockerfile` and `contextDir` paths.

`up` uses `--build-no-cache` to disable build cache. Export flags `--push`,
`--load`, and `--output`, and `--no-cache`, belong to `build`.
`up --update-remote-user-uid-default on` remaps unless the configuration sets
`updateRemoteUserUID: false`; on Linux, `off` remaps only with an explicit `true`;
`never` disables remapping. Linux remaps by default. macOS requires explicit
`--update-remote-user-uid-default on`, even with `updateRemoteUserUID: true`.

`up --skip-post-create` skips all lifecycle commands;
`--skip-post-attach` skips only postAttachCommand. Use
`run-user-commands` to run lifecycle commands in an existing devcontainer.
Use `set-up --container-id <id> --workspace-folder .` to apply the
configuration's lifecycle commands to an existing container. These commands
use lifecycle markers to avoid repeating completed creation/start hooks.
Their `phasesRun` result lists executed phases as objects with `hook` (the
upstream name, such as `postAttachCommand`) and `origin` (such as
`devcontainer.json` or the feature reference). No executed phases gives `[]`.

`dcb version` and `dcb --version` return the version. `dcb skills --list`
lists bundled skills; `dcb skills dcb` prints this skill in the result's `skills[].content`.
`dcb skills install --dir .agents/skills` writes `dcb/SKILL.md` below the
chosen directory.

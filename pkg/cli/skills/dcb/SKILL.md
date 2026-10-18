---
name: dcb
description: Build and manage development containers with dcb, inspect configuration, run container commands and lifecycle hooks, or stage features and VS Code server archives.
relationships:
  references: [CONTRIBUTING]
---
# Development containers with dcb

Use `dcb --help` to read command descriptions, common flags, and examples.
Help is a JSON result: read its `help` string. Use `dcb <command> --help`
for command flags. Use `--workspace-folder` for the local workspace and
`--config` for an explicit devcontainer.json. `--override-config` supplies
alternate content while relative paths stay based on `--config` (or the
workspace configuration path). Flags can follow positional
arguments. Put `--` before the container command's own flags.

Every invocation emits one JSON object on one stdout line. Check `outcome`:
`success` or `error`. Errors include `message` and `description`. Logs and
container command output go to stderr. Use `--log-format json` for JSON
log lines. To capture a container command's output, capture stderr separately.
Exit codes are 0 for success, 1 for an operation failure, 2 for an invalid
invocation. `exec` returns the container command's nonzero exit code.

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

`up --skip-post-create` skips all lifecycle commands;
`--skip-post-attach` skips only postAttachCommand. Use
`run-user-commands` to run lifecycle commands in an existing devcontainer.
Use `set-up --container-id <id> --workspace-folder .` to apply the
configuration's lifecycle commands to an existing container. These commands
use lifecycle markers to avoid repeating completed creation/start hooks.

`dcb version` returns the version. `dcb skills --list` lists bundled skills;
`dcb skills dcb` prints this skill in the result's `skills[].content`.
`dcb skills install --dir .agents/skills` writes `dcb/SKILL.md` below the
chosen directory.

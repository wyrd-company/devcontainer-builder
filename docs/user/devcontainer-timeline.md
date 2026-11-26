---
title: Dev container build and startup timeline
product: dcb
relationships:
  references: [ overview, usage, configuration ]
---

`dcb build` produces an image. On a fresh `dcb up`, the CLI initializes the
host workspace, prepares the image, creates and starts the container, then
runs lifecycle commands inside it. The developer account usually comes from
the image, and the workspace mount is available when the container starts.

| Order | Where | What happens |
| --- | --- | --- |
| 1. Initialize | Host workspace | `up` reads the configuration and runs `initializeCommand` if set. |
| 2. Prepare the image | Image builder | Use the specified image or build a Dockerfile or Compose service image. Install Features on top. |
| 3. Create the container | Docker Engine | Apply the merged runtime settings, including mounts, environment variables and the startup user. |
| 4. Start the container | Container | Mounts become available. The startup process runs Feature entrypoints and any preserved image or service command. |
| 5. Set up the workspace | Container workspace | In single-container mode, match UID/GID if enabled. Then run the creation, start and attach hooks as `remoteUser`. |

## Initialize on the host

`initializeCommand` runs in the local workspace with the host process's
account and environment. `dcb up` runs it once per invocation, including when
reusing a container or using `--no-start`. This is where a configuration can
prepare a host directory that it will bind-mount later. `dcb build` goes
straight to image preparation.

## Prepare the image and install Features

`image` names a starting image. `build.dockerfile` builds the starting image
from a Dockerfile. With Compose, the service named by `service` supplies the
starting image from its `image` or `build` settings.

If the configuration adds Features, `dcb` extends that starting image. Each
Feature's `install.sh` runs as root in a separate build step, in the resolved
installation order. Installed tools, accounts and files become part of the
resulting image. Dockerfile instructions and Feature installers can access
files supplied to the build. The live workspace and other runtime mounts
become available later.

A Feature can also declare mounts, a startup entrypoint and lifecycle hooks.
These settings travel in the image's `devcontainer.metadata` label. At
container creation, `dcb` combines the image metadata with your
`devcontainer.json`. A prebuilt image can therefore arrive with both installed
tools and runtime settings.

The account used for development can already exist in the starting image or
be created by a Dockerfile instruction such as `RUN useradd ...` or by a
Feature such as `common-utils`. A Feature installer receives the intended
developer user in `_REMOTE_USER` and still runs as root. Build-time account
creation makes that user available to startup and the first lifecycle hook.

[Feature installation order](https://containers.dev/implementors/features/#installation-order)
· [Image metadata](https://containers.dev/implementors/spec/#image-metadata)

## Create the container and configure mounts

In single-container mode, `dcb` defaults to binding the Git working tree at
`/workspaces/<repository name>`, with the workspace at its corresponding
path inside that mount. Outside a Git working tree, it binds the workspace
folder at `/workspaces/<folder name>`. `workspaceMount` can override the
mount, and `workspaceFolder` selects the directory for tool commands inside
the container. With Compose, the Compose file supplies the workspace mount
and `service` selects the container that runs the hooks.

`mounts` adds paths beyond the workspace, including mounts declared by
Features. A bind mount exposes a path on the Docker Engine host, which can be
a different machine from the one running `dcb`. A named volume stores data
that can be reused by a replacement container. A mount hides existing image
files at its target, so a workspace bind can hide files installed there
during the image build.

[Mount configuration](configuration.md#mounts)
· [Docker bind mounts](https://docs.docker.com/engine/storage/bind-mounts/)
· [Docker volumes](https://docs.docker.com/engine/storage/volumes/)

## Start the container and resolve the runtime user

- **Startup identity.** `containerUser` selects the user for the main process,
  including Feature entrypoints. It falls back to Compose's `user` in Compose
  mode, then the image's `USER`, then root.
- **Development identity.** `remoteUser` selects the user for lifecycle
  commands and `dcb exec`. It defaults to the container's user. Both settings
  select accounts that already exist.

For example, with `containerUser` set to `root` and `remoteUser` set to
`vscode`, a Feature entrypoint starts as root and `postCreateCommand` runs as
`vscode`.

Feature entrypoints run at startup. With `overrideCommand: false`, the
container then runs the image or Compose service's entrypoint and command.
Single-container configurations default to using a keepalive command.
Compose defaults to preserving the service command.

Matching account IDs helps the remote user write files in Linux bind mounts
with the expected ownership. Single-container `dcb up` enables matching by
default on Linux and adjusts an eligible account after Docker starts the
container, before lifecycle hooks run. The reference Dev Container CLI
adjusts the image before creating the container. Under `dcb`, a startup
process can therefore begin with the account's original numeric IDs.

[Container and remote users](configuration.md#container-and-remote-users)
· [Remote user UID](configuration.md#remote-user-uid)
· [Dev Container creation sequence](https://containers.dev/implementors/spec/#container-creation)

## Run lifecycle commands in the workspace

After Docker starts the container, `dcb` runs the configured hooks in
`workspaceFolder` as `remoteUser`, with `remoteEnv` applied.
`containerEnv` is already available to the startup process.

| Order | Hook | When it runs |
| --- | --- | --- |
| 1 | `onCreateCommand` | On the first setup of a new container. |
| 2 | `updateContentCommand` | After `onCreateCommand`. `dcb up --prebuild` reruns this hook on reuse and stops here. |
| 3 | `postCreateCommand` | After content setup, once per container in ordinary use. |
| 4 | `postStartCommand` | After each start, including the first. |
| 5 | `postAttachCommand` | On tool attach, after the start hook. |

If configured, `dcb` installs dotfiles after `postCreateCommand` and before
`postStartCommand`.

For each hook, Feature commands run in Feature installation order, followed
by the command in `devcontainer.json`. Named commands within a single object
run in parallel. A hook failure stops the remaining hooks for that invocation.
Plain `dcb up` waits for the full sequence. With
`--skip-non-blocking-commands`, it stops at the configured `waitFor` hook
(`updateContentCommand` by default).

On reuse, lifecycle markers skip creation hooks and any start hook already
recorded for that start. A restart runs the start hook again. Plain `dcb up`
runs `postAttachCommand` even when the container was already running. Use
`--skip-post-attach` to omit that hook.

Replacing the container gives it a fresh lifecycle. `dcb up --no-start` ends
after container creation, before startup and the in-container hooks. A later
`dcb up` can start and set up the same container.

[Lifecycle controls](usage.md#lifecycle-controls)
· [Dotfiles](usage.md#dotfiles)
· [Create before first start](usage.md#create-before-first-start)

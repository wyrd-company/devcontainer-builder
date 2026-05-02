# devcontainer-builder

A Go toolkit for working with [devcontainer-spec][spec] images and
containers, with no Node toolchain required.

The packages are designed to be embedded — drop them into a service, an
HTTP handler, or a Kubernetes operator — with thin CLIs on top for the
"I just want a binary" case. Each CLI is a UI; the libraries are the
substance.

| Package | What it does |
| ------- | ------------ |
| `pkg/builder` | Build the image. Authors a BuildKit LLB graph directly so feature installs and the VS Code server are cacheable vertices; supports multi-platform, manifest-list builds, and chains the builtin `dockerfile.v0` frontend for upstream Dockerfile bases. A `docker buildx` shell-out engine is included as a fallback. |
| `pkg/host/docker` | Run the image. Talks to the Docker Engine API directly (no `docker` CLI shell-out), creates / starts / execs / removes containers, runs the lifecycle phases (`onCreate` → `postAttach`) gated by upstream-compatible marker files. Embeddable in a service. |
| `pkg/lifecycle` | Pure planning: returns the ordered phases for a spec + features. The same plan can be executed imperatively (docker) or projected declaratively (kubernetes, future phase). |
| `pkg/substitute` | `${localEnv:VAR}`, `${localWorkspaceFolder}`, `${containerEnv:VAR}`, `${devcontainerId}`, and the rest of the devcontainer spec's variable substitution rules. |

Marker file paths, container labels, and command-form semantics mirror
the upstream [`devcontainers/cli`][upstream] (added as a submodule under
`reference/`) so a container created here can be picked up by upstream
tooling and vice versa.

[spec]: https://containers.dev/
[upstream]: https://github.com/devcontainers/cli

## Install

```sh
go install github.com/wyrd-company/devcontainer-builder/cmd/devcontainer-builder@latest
go install github.com/wyrd-company/devcontainer-builder/cmd/devcontainer-docker@latest
```

## CLIs

Two binaries: one for building images, one for running them. They share
the same library packages.

```
devcontainer-builder <command> [flags]

Commands:
  build      Build a devcontainer image
  generate   Write the wrapper Dockerfile + build context, do not build
  features   Resolve and stage devcontainer features into a directory
  server     Download a VS Code server tarball
  version    Print version information
```

```
devcontainer-docker <command> [flags]

Commands:
  up       Build (if needed), create, start, and run lifecycle commands
  exec     Run a command in the running devcontainer
  down     Stop and remove the devcontainer
  inspect  Print devcontainer state
  version  Print version
```

### Examples

Build the devcontainer in `./` and load it into the local docker store:

```sh
devcontainer-builder build \
  --workspace . \
  --tag my-org/devbox:latest \
  --load
```

Push a multi-arch-ready single-platform build to a registry, using a
specific buildkitd endpoint:

```sh
BUILDKIT_HOST=tcp://buildkit.internal:1234 \
devcontainer-builder build \
  --workspace . \
  --tag registry.internal/devbox:1.2.3 \
  --push \
  --platform linux/amd64
```

Generate the wrapper Dockerfile for inspection without running a build:

```sh
devcontainer-builder generate --workspace . --skip-vscode-server
```

Force the buildx fallback engine:

```sh
devcontainer-builder build --engine buildx --tag dev:latest --load
```

## Architecture

```
                      ┌──────────────────────────┐
                      │  devcontainer.json       │
                      │  (with JSONC support)    │
                      └────────────┬─────────────┘
                                   │
              ┌────────────────────┼────────────────────┐
              ▼                    ▼                    ▼
   ┌──────────────────┐  ┌──────────────────┐  ┌──────────────────┐
   │ pkg/features     │  │ pkg/vscode       │  │ pkg/dockerfile   │
   │ - OCI pull       │  │ - resolve commit │  │ - wrapper        │
   │ - extract        │  │ - download tar   │  │   Dockerfile     │
   │ - install order  │  │ - sha256         │  │   (always emit)  │
   └────────┬─────────┘  └────────┬─────────┘  └──────────────────┘
            └─────────┬───────────┘
                      ▼
       ┌──────────────────────────────┐
       │      pkg/builder             │
       │  orchestrates the pipeline   │
       └──────────────┬───────────────┘
                      │
        ┌─────────────┴─────────────┐
        ▼                           ▼
   ┌─────────────┐          ┌──────────────┐
   │ pkg/buildkit │  default  │ pkg/buildx   │  fallback
   │ (LLB graph)  │          │ (docker CLI) │
   └─────────────┘          └──────────────┘
```

### Package map

| Package           | Purpose                                                         |
| ----------------- | --------------------------------------------------------------- |
| `pkg/spec`        | `devcontainer.json` types + JSONC parser                        |
| `pkg/oci`         | Minimal OCI Distribution v2 client (anonymous + bearer auth)    |
| `pkg/features`    | Feature resolution from OCI/local + install ordering            |
| `pkg/vscode`      | VS Code server resolver + downloader (Microsoft update API)     |
| `pkg/dockerfile`  | Wrapper Dockerfile generator                                    |
| `pkg/buildkit`    | LLB graph authoring + `buildkit/client` solve                   |
| `pkg/buildx`      | Shell wrapper over `docker buildx build`                        |
| `pkg/platform`    | Platform/arch translation                                       |
| `pkg/builder`     | Top-level orchestrator                                          |
| `cmd/devcontainer-builder` | CLI entrypoint                                         |

### How features are applied

Each feature becomes its own LLB exec vertex:

```go
state = state.Run(
    llb.User("root"),
    llb.Args([]string{"/bin/sh", "-c", "cd /tmp/build-features/<id>; ./install.sh"}),
    llb.AddMount("/tmp/build-features/<id>", localCtx, llb.SourcePath("_features/<id>"), llb.Readonly),
    // option env vars (VERSION=20, etc.)
).Root()
```

Features whose options or contents haven't changed stay cached on
rebuilds; only the affected feature and its downstream installs are
re-executed.

### How the VS Code server is injected

The server tarball is fetched as a content-addressable HTTP source:

```go
tarball := llb.HTTP(serverURL,
    llb.Filename("vscode-server.tar.gz"),
    llb.Checksum("sha256:..."),
)
state = state.Run(
    llb.AddMount("/tmp/dl", tarball, llb.Readonly),
    llb.Args([]string{"/bin/sh", "-c",
        "mkdir -p ~/.vscode-server/bin/<commit>; tar -xzf /tmp/dl/vscode-server.tar.gz ..."}),
).Root()
```

Because BuildKit caches by checksum, the tarball download is reused as
long as the resolved commit is the same. The unpack target is the
canonical path VS Code looks for (`~/.vscode-server/bin/<commit>/`),
so the first remote attach finds the server already in place.

## Library usage

### Building an image

```go
import (
    "context"

    "github.com/wyrd-company/devcontainer-builder/pkg/builder"
)

res, err := builder.New().Build(ctx, builder.Options{
    Workspace: ".",
    Tags:      []string{"my-org/devbox:latest"},
    Load:      true,
})
```

### Running a container against the docker engine

The docker host is built to be embedded — the constructor takes a
`*client.Client` so a service or operator can manage its connection
lifetime. The package never reads `$DOCKER_HOST`, never writes to
stdout/stderr unless explicitly given writers, and never touches global
state.

```go
import (
    "github.com/docker/docker/client"
    "github.com/wyrd-company/devcontainer-builder/pkg/host/docker"
    "github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

cli, _ := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
host := docker.New(cli, docker.Options{Stdout: os.Stdout, Stderr: os.Stderr})

dc, _ := spec.LoadFromWorkspace("/path/to/workspace")
res, err := host.Up(ctx, docker.UpRequest{
    Spec:           dc,
    SpecPath:       dc.Path,
    LocalWorkspace: "/path/to/workspace",
    Image:          "my-org/devbox:latest",
})
```

`Up` is idempotent: it discovers an existing devcontainer for the
workspace via the `devcontainer.local_folder` label, starts it if
stopped, and runs only the lifecycle phases whose marker files indicate
they need to.

The structured spec fields (`mounts`, `containerEnv`, `init`,
`privileged`, `capAdd`, `securityOpt`, `containerUser`) and the free-form
`runArgs` array are both honored. `runArgs` is parsed against the docker
CLI flag vocabulary — `--network`, `--cap-add`, `--gpus`, `--device`,
`-v` / `--mount`, `-e KEY=VAL`, `-p`, `--ulimit`, `--shm-size`,
`--memory`, `--cpus`, namespace flags (`--ipc`, `--pid`, `--uts`,
`--userns`, `--cgroupns`), restart policy, sysctls, tmpfs, and the
common booleans (`--init`, `--privileged`, `--read-only`) — and folded
into the engine API call. List flags such as `--cap-add` append to
their structured-spec counterparts rather than replacing them. Unknown
flags are rejected so a typo doesn't silently change container
behavior. The `--entrypoint` flag is rejected because the host owns the
keepalive entrypoint that holds the container open.

The lower-level packages (`pkg/buildkit`, `pkg/dockerfile`,
`pkg/features`, `pkg/vscode`, `pkg/lifecycle`, `pkg/substitute`) are
stable enough to use directly when you want to assemble a different
pipeline.

### Multi-platform builds

Pass `--platform` more than once to produce a manifest-list image. Each
platform's LLB graph is authored with the right `llb.Platform`
constraint so cross-arch feature installs run under emulation (or on a
matching native worker, when available) and not against the daemon's
default arch.

```sh
devcontainer-builder build \
  --workspace . \
  --tag registry.internal/devbox:1.2.3 \
  --platform linux/amd64 \
  --platform linux/arm64 \
  --push
```

`--load` is single-platform only — docker's image store can't hold a
manifest list. Use `--push` (image exporter) or `--output` (filesystem
exporter) for multi-platform builds.

### Upstream Dockerfile builds (BuildKit engine)

When the spec uses `dockerFile` (or `build.dockerfile`), the BuildKit
engine chains the builtin `dockerfile.v0` frontend inside the same
gateway invocation that authors the rest of the LLB graph. The upstream
build's reference becomes the base of the devcontainer overlays
directly — no `docker build` round-trip, no temporary tag.

The buildx engine still pre-builds the upstream Dockerfile through
`docker buildx build --load` and then references the resulting tag.

## Status

Known gaps:

- **Kubernetes host.** Planned. The pure `pkg/lifecycle` package
  produces an ordered phase plan that's ready to be projected into
  Pod / Deployment manifests by a future `pkg/projection/kubernetes`,
  with a separate `cmd/devcontainer-k8s` CLI for the rendering UI.
- **`dockerComposeFile`.** Multi-container compose-style devcontainers
  aren't supported.
- **`updateRemoteUserUID`.** UID rewriting to match the host user (to
  avoid bind-mount permission surprises) is on the roadmap; for now
  containers run as the spec-declared `remoteUser` without UID
  remapping.
- **`forwardPorts` / `appPort`.** Not yet wired through to docker port
  publishing.

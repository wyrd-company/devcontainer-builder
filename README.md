# devcontainer-builder

Go package and CLI that builds [devcontainer-spec][spec] images by
authoring a BuildKit LLB graph directly. Devcontainer features are
pulled from OCI registries and applied as cacheable build steps; the
VS Code server is fetched via `llb.HTTP` so it gets its own
content-addressable cache vertex and can be reused across rebuilds.

A buildx shell-out engine is included as a fallback for environments
that don't have a reachable `buildkitd`.

[spec]: https://containers.dev/

## Install

```sh
go install github.com/wyrd-company/devcontainer-builder/cmd/devcontainer-builder@latest
```

## CLI

```
devcontainer-builder <command> [flags]

Commands:
  build      Build a devcontainer image
  generate   Write the wrapper Dockerfile + build context, do not build
  features   Resolve and stage devcontainer features into a directory
  server     Download a VS Code server tarball
  version    Print version information
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

```go
import (
    "context"

    "github.com/wyrd-company/devcontainer-builder/pkg/builder"
)

func main() {
    res, err := builder.New().Build(context.Background(), builder.Options{
        Workspace: ".",
        Tags:      []string{"my-org/devbox:latest"},
        Load:      true,
    })
    if err != nil {
        panic(err)
    }
    _ = res
}
```

The lower-level packages (`pkg/buildkit`, `pkg/dockerfile`,
`pkg/features`, `pkg/vscode`) are stable enough to use directly when
you want to assemble a different pipeline.

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

Initial implementation. Known gaps:

- Lifecycle commands (`postCreateCommand`, etc.) are parsed but not
  executed at build time. They belong to the run-time start phase
  rather than the image build, and need a separate `up`/`exec`
  command path that this tool intentionally does not own.

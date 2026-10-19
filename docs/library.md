---
title: Go libraries
order: 5
docs: true
relationships:
  references: [ configuration ]
---

`dcb` is a thin command over Go packages in
`github.com/wyrd-company/devcontainer-builder`. The host packages take their
Docker or Kubernetes client from the caller, do not read `DOCKER_HOST` or
`KUBECONFIG`, and write nothing to stdout or stderr unless given a writer, so
a service or an operator can embed them.

| Package | Purpose |
| --- | --- |
| `pkg/builder` | Builds an image from a configuration. |
| `pkg/host/docker` | Creates, starts, runs lifecycle commands in, execs into, and removes a container through the Docker Engine API. |
| `pkg/host/dockercompose` | The same for a Docker Compose configuration. |
| `pkg/host/kubernetes` | The same for a Pod, through the Kubernetes API. |
| `pkg/projection/kubernetes` | Converts a configuration into a Pod and its volumes, with no API calls. |
| `pkg/lifecycle` | Plans the lifecycle commands of a configuration and its features. |
| `pkg/host/runner` | Runs a lifecycle plan with marker files through any exec transport. |
| `pkg/spec` | Reads `devcontainer.json`. |
| `pkg/features` | Resolves features and their install order. |
| `pkg/substitute` | Substitutes configuration variables. |
| `pkg/registryauth` | Reads registry credentials from the Docker client configuration. |

Package documentation is on
[pkg.go.dev](https://pkg.go.dev/github.com/wyrd-company/devcontainer-builder).

## Build an image

```go
import "github.com/wyrd-company/devcontainer-builder/pkg/builder"

res, err := builder.New().Build(ctx, builder.Options{
    Workspace: "/path/to/workspace",
    Tags:      []string{"example:dev"},
    Load:      true,
})
```

`Options.Docker` takes an existing Engine API client, and
`Options.BuildKitAddress` selects a separate BuildKit. Builds read registry
credentials from the Docker client configuration. See
[Registry credentials](/docs/dcb/configuration#registry-credentials).

## Run a container

```go
import (
    "github.com/docker/docker/client"
    "github.com/wyrd-company/devcontainer-builder/pkg/host/docker"
    "github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
host := docker.New(cli, docker.Options{Stdout: os.Stdout, Stderr: os.Stderr})

dc, err := spec.LoadFromWorkspace("/path/to/workspace")
res, err := host.Up(ctx, docker.UpRequest{
    Spec:           dc,
    SpecPath:       dc.Path,
    LocalWorkspace: "/path/to/workspace",
    Image:          "example:dev",
    ImageBuilt:     true,
})
```

`ImageBuilt` tells `Up` that the image was built from this configuration, so
its metadata label already holds the feature and configuration entries.

`RemoteEnv` and `Secrets` add variables to the lifecycle commands and the
dotfiles install of one `Up`. The configuration's `remoteEnv` wins over
`RemoteEnv`, and `Secrets` win over both. `Up` does not store them, and it
does not mask them: lifecycle output reaches `Options.Stdout` and
`Options.Stderr` as the commands print it. Mask secret values in those
writers if the output is shown or kept.

`Up` pulls a missing image anonymously. To send registry credentials, set
`docker.Options.RegistryAuth`, for example to `registryauth.FromEnvironment()`
from `pkg/registryauth`, which reads the Docker client configuration.

`Up` is idempotent. It finds the workspace container by its labels, starts it
if it is stopped, and runs only the lifecycle commands that the marker files
show as not yet done.

## Project into Kubernetes

```go
import "github.com/wyrd-company/devcontainer-builder/pkg/projection/kubernetes"

pj, err := kubernetes.Project(kubernetes.ProjectRequest{
    Spec:           dc,
    SpecPath:       dc.Path,
    LocalWorkspace: "/path/to/workspace",
    Image:          "registry.example.com/team/dev:1.0",
    Name:           "dev",
    Namespace:      "default",
})
// pj.Pod is a *corev1.Pod.
```

`runArgs` flags without a Kubernetes equivalent are errors and are not
dropped. Lifecycle commands are not part of the Pod; `pkg/host/kubernetes`
runs them through the Pod exec API. `ProjectCompose` converts a Compose
configuration into one Pod with a container for each service.

## How a build works

`pkg/builder` writes a BuildKit LLB graph directly. Each feature is one exec
step: its files, its option values, and an install wrapper are copied into a
scratch layer, mounted at `/tmp/dev-container-features/<id>`, and the wrapper
runs `install.sh` as root:

```go
content := llb.Scratch().File(
    llb.Copy(localCtx, "_features/<id>", "/",
        &llb.CopyInfo{CopyDirContentsOnly: true}).
        Mkfile("/devcontainer-features.env", 0o644, optionValues).
        Mkfile("/devcontainer-features-install.sh", 0o755, installWrapper),
)
state = state.Run(
    llb.User("root"),
    llb.Args([]string{"/bin/sh", "-c",
        "cd /tmp/dev-container-features/<id> && ./devcontainer-features-install.sh"}),
    llb.AddMount("/tmp/dev-container-features/<id>", content),
).Root()
```

A feature whose files and options do not change stays cached. A change runs
that feature again, and the features installed after it.

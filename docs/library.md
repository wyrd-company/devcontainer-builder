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
a service or an operator can embed them. The packages read files with the
credentials that the calling process has at the time of the call; see
[Process credentials](/docs/dcb/overview#process-credentials).

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
| `pkg/workspace` | Places a workspace folder in a container: its Git root, a worktree's common Git folder, and the workspace folder. |
| `pkg/registryauth` | Reads registry credentials from the Docker client configuration, or holds credentials that the caller supplies. |

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
credentials from the Docker client configuration unless
`Options.RegistryAuth` is set. See [Registry credentials](#registry-credentials).

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
Without `ImageBuilt`, the Docker and Compose hosts add each feature's
`containerEnv` to the container, and `pkg/projection/kubernetes` adds it to
the Pod. References to other environment variables, such as `${PATH}`, are not
expanded there; they are expanded only when the features are built into the
image.

`RemoteEnv` and `Secrets` add variables to the lifecycle commands and the
dotfiles install of one `Up`. The configuration's `remoteEnv` wins over
`RemoteEnv`, and `Secrets` win over both. `Up` does not store them, and it
does not mask them: lifecycle output reaches `Options.Stdout` and
`Options.Stderr` as the commands print it. Mask secret values in those
writers if the output is shown or kept.

`Up` pulls a missing image anonymously. To send registry credentials, set
`docker.Options.RegistryAuth`. See [Registry credentials](#registry-credentials).

`Up` is idempotent. It finds the workspace container by its labels, starts it
if it is stopped, and runs only the lifecycle commands that the marker files
show as not yet done.

## Place a workspace

```go
import "github.com/wyrd-company/devcontainer-builder/pkg/workspace"

placement, err := workspace.Resolve("/path/to/workspace/app", false, false, workspace.Options{
    MountGitRoot: true,
    Platform:     runtime.GOOS,
    GitPrefix:    "/usr",
})
```

`Resolve` finds the Git root as `dcb up` does, without running `git`, and
returns the workspace mount and folder that a host's `UpRequest` takes.
`Options.GitPrefix` is the prefix of the host Git's build, as `--git-prefix`
names it; without it, an `include.path` of `%(prefix)/` followed by a relative
path is not read. A relative `Options.GitPrefix` starts at the including
file's folder, not at the current folder as a relative `--git-prefix` does.
`Options.GitRoot` returns the Git root alone. Repository-format
config files can be streams, such as FIFOs. Discovery waits for a writer
without a time limit, and reads the file again after setup, as Git does.
Discovery also reads the
command-line config environment (`GIT_CONFIG_COUNT` pairs and
`GIT_CONFIG_PARAMETERS`) after repository setup. A command-line
`include.path` must expand to an absolute path. After all full-read inputs,
an invalid `core.ignorecase`, `core.filemode`, `core.symlinks`,
`core.trustctime`, `core.quotepath` or `core.ignorestat` boolean gives no Git root,
so `Resolve` mounts the requested folder. See [Workspace mounts](/docs/dcb/usage#workspace-mounts-and-container-selection).

## Registry credentials

One credential source can serve every registry request of a workspace:

```go
import "github.com/wyrd-company/devcontainer-builder/pkg/registryauth"

auth := registryauth.FromEnvironment()

// Image pulls.
docker.New(cli, docker.Options{RegistryAuth: auth})
// Image pulls and service builds.
dockercompose.New(cli, dockercompose.Options{RegistryAuth: auth})
// BuildKit pulls and pushes, feature downloads, and base image reads.
builder.New().Build(ctx, builder.Options{RegistryAuth: auth})
dockercompose.BuildPrimaryImage(ctx, dockercompose.BuildPrimaryInputs{
    BuilderOptions: builder.Options{RegistryAuth: auth},
})
```

| Source | Reads | Runs a credential helper |
| --- | --- | --- |
| `registryauth.FromEnvironment()`, `registryauth.New(dir)` | `DOCKER_AUTH_CONFIG`, then `config.json`, as the `docker` command does | Yes, for `credsStore` and `credHelpers` |
| `registryauth.NewWithoutHelpers(dir)` | The same, in the same order | No |
| `registryauth.Static{"ghcr.io": {...}}` | Nothing: the credentials that the caller gives | No |

`NewWithoutHelpers` reads `DOCKER_AUTH_CONFIG` first, then the `auths` of
`config.json`. A registry that `credsStore` or `credHelpers` gives to a helper
fails with `registryauth.ErrHelpersDisabled`, as a failing helper fails; its
`auths` entry is not used, because the `docker` command would not use it. The
error stays a match for `errors.Is` in the errors of builds, feature downloads,
and image pulls.

`Static` holds credentials by registry host, such as `ghcr.io` or
`127.0.0.1:5000`; the Docker Hub names `docker.io`, `index.docker.io`,
`registry-1.docker.io`, and `https://index.docker.io/v1/` are one registry. A
host that it does not hold is anonymous, so `registryauth.Static{}` is
anonymous for every registry.

When `builder.Options.RegistryAuth` is nil, BuildKit reads the Docker client
configuration, and feature downloads and base image reads use the
`Builder.Resolver.OCI` client. When `dockercompose.Options.RegistryAuth` gives
only `EngineAuth`, service builds get credentials from its `EngineAuth` values,
and never from the Docker client configuration.

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

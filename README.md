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
| `pkg/host/dockercompose` | Run a compose-mode devcontainer (`dockerComposeFile` + `service`). Parses and merges compose files via `compose-spec/compose-go`, generates an in-memory override that injects the workspace mount + remoteEnv + the feature-derived primary image, and creates networks / volumes / containers natively through the engine API — no `docker compose` CLI shell-out. Lifecycle phases run inside the primary service container. |
| `pkg/host/kubernetes` | Run the image on a cluster. Discover-or-create the Pod via the apiserver, wait for the target container to reach Ready, exec lifecycle phases through the pod-exec subresource (SPDY), port-forward, delete on teardown. Builds on `pkg/projection/kubernetes` for the spec → Pod translation and `pkg/host/runner` for marker gating. No CLI: this is a library for operators and services that already own a `kubernetes.Interface`. |
| `pkg/host/runner` | Transport-agnostic lifecycle executor: takes an `Execer` (docker exec, apiserver pod-exec, anything that runs argv inside a running container) plus a `lifecycle.Plan` and runs the phases with marker-file gating. Owns the marker shell, the Shell/Args/Parallel dispatch, and per-parallel output buffering. Used by both `pkg/host/docker` and `pkg/host/kubernetes`. |
| `pkg/lifecycle` | Pure planning: returns the ordered phases for a spec + features. The same plan can be executed imperatively (docker, kubernetes) or projected declaratively. |
| `pkg/projection/kubernetes` | Pure config translation: a `*spec.DevContainer` + features become a `*corev1.Pod` (and friends). For compose-mode specs, `ProjectCompose` emits a single multi-container Pod (sidecar model) plus PVCs for compose named volumes. No client-go, no apiserver round-trips — drop the result into an operator's reconcile, render to YAML, or hand to `pkg/host/kubernetes`. |
| `pkg/substitute` | `${localEnv:VAR}`, `${localWorkspaceFolder}`, `${containerEnv:VAR}`, `${devcontainerId}`, and the rest of the devcontainer spec's variable substitution rules. |

Marker file paths, container labels, and command-form semantics mirror
the upstream [`devcontainers/cli`][upstream] (added as a submodule under
`reference/`) so a container created here can be picked up by upstream
tooling and vice versa.

[spec]: https://containers.dev/
[upstream]: https://github.com/devcontainers/cli

## Install

```sh
go install github.com/wyrd-company/devcontainer-builder/cmd/dcb@latest
go install github.com/wyrd-company/devcontainer-builder/cmd/devcontainer-docker@latest
```

## CLIs

Two binaries: one for building images, one for running them. They share
the same library packages.

```
dcb <command> [flags]

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
dcb build \
  --workspace . \
  --tag my-org/devbox:latest \
  --load
```

Push a multi-arch-ready single-platform build to a registry, using a
specific buildkitd endpoint:

```sh
BUILDKIT_HOST=tcp://buildkit.internal:1234 \
dcb build \
  --workspace . \
  --tag registry.internal/devbox:1.2.3 \
  --push \
  --platform linux/amd64
```

Generate the wrapper Dockerfile for inspection without running a build:

```sh
dcb generate --workspace . --skip-vscode-server
```

Force the buildx fallback engine:

```sh
dcb build --engine buildx --tag dev:latest --load
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
| `cmd/dcb`         | CLI entrypoint                                                  |

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

`forwardPorts` and `appPort` are translated to docker port publishing.
Bare port numbers (`3000`, `"3000"`, `"3000/udp"`, `"8000-8002"`) bind
to `127.0.0.1:N:N` so listeners stay on loopback by default; entries
already in `host:container` or `iface:host:container` form are
honored verbatim. Both merge with any `-p` / `--publish` entries in
`runArgs` rather than replacing them.

`updateRemoteUserUID` is honored: when the embedder passes
`UpRequest.RemoteUserUIDUpdate{HostUID, HostGID}` (and the spec
doesn't override with `updateRemoteUserUID: false`), the host execs
into the started container as root and sed-rewrites `/etc/passwd` +
`/etc/group` so the configured `remoteUser`'s UID/GID match the
host's, so files created in bind-mounted host paths land with the
right ownership. The runtime behavior matches upstream's
`scripts/updateUID.Dockerfile` — same skip cases (`remoteUser` is
`root` or numeric, IDs already match, a colliding UID/GID exists) —
but no rebuild and no extra image tag. The package never reads
`os.Getuid` itself; embedders supply the IDs they want applied. The
`devcontainer-docker up` CLI calls this on linux/macOS by default;
pass `--update-remote-user-uid=off` to disable.

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
dcb build \
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

### Projecting a spec into a Kubernetes Pod

`pkg/projection/kubernetes` mirrors the field mapping that
`pkg/host/docker` does against the docker engine, but emits
`*corev1.Pod` (and any required volumes / ports) instead of an
imperative engine API call. The package is pure — no client-go, no
apiserver round-trips — so an operator's reconcile loop can call it
inline and apply the result with controller-runtime, kubectl, or a
manifest renderer.

```go
import (
    "github.com/wyrd-company/devcontainer-builder/pkg/projection/kubernetes"
    "github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

dc, _ := spec.LoadFromWorkspace("/path/to/workspace")
pj, err := kubernetes.Project(kubernetes.ProjectRequest{
    Spec:           dc,
    SpecPath:       dc.Path,
    LocalWorkspace: "/path/to/workspace",
    Image:          "registry.example.com/devbox:1.2.3",
    Name:           "alice-devbox",
    Namespace:      "dev",
})
// pj.Pod is a fully-rendered *corev1.Pod ready to apply.
```

`runArgs` is parsed against the same docker-CLI vocabulary the docker
host accepts. Flags that map cleanly land on `PodSpec` /
`Container.SecurityContext` / `Container.Resources` (`--cap-add`,
`--privileged`, `--user`, `--memory`, `--cpus`, `--shm-size`,
`--sysctl`, `--group-add`, `--add-host`, `--dns`, `-v` / `--mount`,
`--network=host`, `--restart`, `--runtime`, `--gpus`, …). Flags
without a clean Kubernetes equivalent (`--init`, `--cgroup-parent`,
`--ulimit`, `--gpus device=…`, named docker volumes, the
`iface:host:container` port form, …) return an error rather than
being silently dropped — a missing security flag changes the
container's security model. Embedders that want lenient behavior can
strip the offending flags before calling.

`forwardPorts` and `appPort` collapse to declared `ContainerPort`
entries — Kubernetes pods don't have docker's "publish to localhost"
model, so the embedder is responsible for fronting them with a
Service if external reachability is required. Bare numbers, `N/proto`
strings, and `N-M` ranges all translate; explicit `host:container`
forms are rejected (use `runArgs --publish` for `hostPort`).

The workspace volume defaults to a `HostPath` sourced from
`LocalWorkspace`, matching the docker host's default for single-node
clusters (kind, minikube, docker-desktop). For remote clusters where
the host workspace isn't reachable from the kubelet, pass
`WorkspaceVolumeSource` to plug in a `PersistentVolumeClaim` or any
other volume source.

Lifecycle commands (`onCreate`, `postCreate`, `postStart`,
`postAttach`) are *not* projected into init containers or lifecycle
hooks: those phases need the running container's userspace state and
can't be modeled as static manifest fields.

### Projecting a compose-mode devcontainer into a Kubernetes Pod

`pkg/projection/kubernetes`'s `ProjectCompose` is the compose
counterpart to `Project`: it takes a `*types.Project` (use
`pkg/host/dockercompose.LoadProject` to produce one) plus the
devcontainer.json + primary service name, and returns a single
multi-container Pod (sidecar model) along with any PVCs the Pod's
volume references need.

```go
import (
    composedck "github.com/wyrd-company/devcontainer-builder/pkg/host/dockercompose"
    "github.com/wyrd-company/devcontainer-builder/pkg/projection/kubernetes"
    "github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

dc, _ := spec.LoadFromWorkspace(workspace)
proj, _ := composedck.LoadProject(ctx, composedck.LoadInputs{
    Spec:      dc,
    Workspace: workspace,
})

out, err := kubernetes.ProjectCompose(kubernetes.ComposeProjectRequest{
    Spec:           dc,
    SpecPath:       dc.Path,
    LocalWorkspace: workspace,
    Project:        proj,
    PrimaryImage:   "registry.example.com/devbox-derived:1.2.3",
    Name:           "alice-devbox",
    Namespace:      "dev",
})
// out.PersistentVolumeClaims need to exist before out.Pod is applied —
// the Pod references them by ClaimName.
```

The shape is one Pod with one Container per compose service (the
primary first, sidecars next in stable name order). Compose volumes
translate as:

- **Bind mount** → `HostPath` volume. Same caveat as the
  single-container projection: only useful on single-node clusters
  where the kubelet can see the host path.
- **Named volume (default)** → a generated `PersistentVolumeClaim`
  referenced by the Pod. Default access mode is `ReadWriteOnce` and
  the storage request defaults to `1Gi`; both are tunable via
  `VolumeStorageClass` and `VolumeStorageRequest` on the request.
  When two services bind the same compose volume, they share the
  same Pod-level Volume entry — same PVC, two `volumeMounts`.
- **External named volume (`external: true`)** → a Pod Volume
  referencing a pre-existing PVC named after the compose volume's
  `name:` (or its key). No PVC is generated.
- **Tmpfs** → `EmptyDir{Medium: Memory}`.
- **image / cluster** → returns an error rather than silently
  dropping.

Compose-mode Kubernetes scope cuts:

- **`depends_on`.** Containers in a Pod can't be ordered through
  the compose model; `depends_on` is ignored. Use init containers
  or readiness probes if you need start ordering.
- **Service-name DNS.** Containers in a Pod share `localhost`, so
  cross-service traffic is `127.0.0.1:<port>` rather than the
  compose `<svcname>:<port>` model. Anything that reaches into
  another service by hostname needs the spec restructured (e.g.
  multi-Deployment + Services) — outside this iteration's scope.
- **Compose-side `build:`.** Each service must reference a pullable
  `image:`; `BuildPrimaryImage` from `pkg/host/dockercompose` lets
  you bake features into the primary's derived image, but compose
  `build:` for any service is not honored.
- **Profiles, `watch`, `develop`, `configs`, `secrets`.** Not
  honored — same as the docker compose host.
- **`forwardPorts`/`appPort` vs compose `ports:`.** `forwardPorts`
  and `appPort` are merged onto the primary's ContainerPorts. The
  compose `ports:` field is also translated (one ContainerPort per
  service entry); the docker publish-to-localhost / hostPort
  semantics don't carry over — use a Service for external
  reachability.

`pkg/host/kubernetes` is the imperative companion to
`pkg/projection/kubernetes`. The projection package turns a spec into a
`*corev1.Pod`; the host package takes that translation and drives the
apiserver: discover-or-create the Pod, wait for the target container to
reach Ready, exec lifecycle phases through the pod-exec subresource
(SPDY), port-forward, delete on teardown.

```go
import (
    k8shost "github.com/wyrd-company/devcontainer-builder/pkg/host/kubernetes"
    "github.com/wyrd-company/devcontainer-builder/pkg/spec"
    "k8s.io/client-go/kubernetes"
    "k8s.io/client-go/tools/clientcmd"
)

cfg, _ := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
cs, _ := kubernetes.NewForConfig(cfg)
host := k8shost.New(cs, cfg, k8shost.Options{Stdout: os.Stdout, Stderr: os.Stderr})

dc, _ := spec.LoadFromWorkspace("/path/to/workspace")
res, err := host.Up(ctx, k8shost.UpRequest{
    Spec:           dc,
    SpecPath:       dc.Path,
    LocalWorkspace: "/path/to/workspace",
    Image:          "registry.example.com/devbox:1.2.3",
    Namespace:      "dev",
})
```

`Up` is idempotent. The discovery key is a sha256 of `LocalWorkspace`
written onto the Pod as the `devcontainer.io/local-folder-hash` label
(annotations carry the verbatim path values, mirroring the docker
host's labels — Kubernetes label values can't hold filesystem paths).
A second call rediscovers the Pod by that label, waits for Ready,
and runs only the lifecycle phases whose marker files indicate they
need to. Lifecycle exec uses the same `pkg/host/runner` engine the
docker host uses, so marker contents and phase ordering stay
upstream-compatible across hosts.

There is deliberately no Kubernetes CLI in this repo. Rendering — the
half that benefits from a CLI — is `kubectl apply -f <file>` against
the YAML you serialize from `pkg/projection/kubernetes`. The
imperative half lives where the Kubernetes client already does:
inside an operator's reconcile loop or a service that owns its own
`kubernetes.Interface`. The package never reads `$KUBECONFIG` and
never writes to stdout/stderr unless explicitly given writers.

### Running a compose-mode (multi-container) devcontainer

When a `devcontainer.json` declares `dockerComposeFile` + `service`,
`pkg/host/dockercompose` brings the whole compose project up natively
through the docker engine API. It mirrors what `docker compose up`
would do — same project labels (`com.docker.compose.project`,
`com.docker.compose.service`, …) so a project started here is
discoverable by `docker compose`/`docker ps`/the upstream
devcontainer CLI and vice versa — but never shells out to the
`docker compose` binary.

The package owns the full pipeline:

1. **Parse + merge.** `compose-spec/compose-go/v2` loads every file
   listed in `dockerComposeFile` (in order), interpolates `${VAR}`
   against the supplied `LocalEnv`, and resolves a normalized
   `*types.Project`.
2. **Override.** A small in-memory YAML layer is appended to the
   merge chain, touching only the primary service: it injects the
   workspace bind mount + `working_dir`, the spec's `remoteEnv`, the
   feature-derived image (when `BuildPrimaryImage` produced one), and
   a long-running keepalive entrypoint matching the docker host's so
   exec/lifecycle have a live container regardless of the image's
   `CMD` (suppress with `overrideCommand: false` in the spec).
3. **Project name.** Stable across re-runs: a sanitized basename of
   the workspace path plus an 8-char sha256 prefix of its absolute
   form, so the same workspace always reuses the same project and
   unrelated workspaces never collide.
4. **Networks + volumes.** Anything declared by the project that
   doesn't already exist is created with the standard compose labels.
   `external: true` resources are left alone.
5. **Containers.** Visited in `depends_on` order via
   `Project.ForEachService` and reused if a container with the same
   project + service labels already exists; otherwise created and
   started.
6. **Lifecycle.** Runs against the primary service container only,
   through the shared `pkg/host/runner` marker engine (so
   `onCreate` → `postAttach` semantics, marker file paths, and
   container-env probing all match the docker / kubernetes hosts).

```go
import (
    "github.com/docker/docker/client"
    "github.com/wyrd-company/devcontainer-builder/pkg/host/dockercompose"
    "github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

cli, _ := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
host := dockercompose.New(cli, dockercompose.Options{Stdout: os.Stdout, Stderr: os.Stderr})

dc, _ := spec.LoadFromWorkspace(workspace)

// Optional: bake features into a derived image FROM the primary
// service's compose `image:`. When the spec has no features the
// helper just returns that base image unchanged.
build, _ := dockercompose.BuildPrimaryImage(ctx, dockercompose.BuildPrimaryInputs{
    Spec:      dc,
    Workspace: workspace,
})

res, err := host.Up(ctx, dockercompose.UpRequest{
    Spec:           dc,
    SpecPath:       dc.Path,
    LocalWorkspace: workspace,
    PrimaryImage:   build.Image,
})
// res.PrimaryContainerID is where lifecycle commands ran; res.ContainerIDs
// maps every started service to its container ID.
```

Compose-mode scope cuts in this iteration — handled in a future pass
rather than silently dropped:

- **Profiles, `watch`, `develop`, `configs`, `secrets`.** Not
  honored.
- **Volume types.** Bind and named volumes are translated; tmpfs,
  image, and cluster volume types return an error rather than being
  silently dropped.
- **`forwardPorts` / `appPort` on the primary.** Not layered onto
  the compose service — the user already has compose `ports:` for
  port publishing in this mode.

### Compose-side `build:`

Both the primary service and any sidecar may declare a compose-side
`build:` block. `BuildPrimaryImage` runs the primary's `build:`
through `pkg/buildx` first to produce a base image, then layers
features (and the VS Code server) on top with the regular builder
pipeline. `Up` walks the project for non-primary services with a
`build:` block and builds them through the same path before
bringing them up — the resulting tag is folded into the project
in-memory so the spawn step uses it instead of compose's empty
`image:`. `Options.SkipServiceBuild` opts out for embedders that
manage their own builds. `dockerfile_inline:` is rejected (we don't
shadow a real path with synthetic content); everything else
(`context`, `dockerfile`, `args`, `target`, `cache_from` /
`cache_to`, `platforms`, `tags`, `no_cache`, `pull`, `labels`)
maps directly onto the buildx invocation. When a service declares
both `build:` and `image:`, the `image:` becomes the primary tag
applied to the build (matching `docker compose build`).

### Recreate-on-config-change

Every container created by `Up` carries a
`com.docker.compose.config-hash` label computed from a normalized
JSON snapshot of its compose `ServiceConfig` (image, entrypoint,
command, env, volumes, ports, networks, depends_on, security
flags, …). Host-managed labels — anything under
`com.docker.compose.*` or `devcontainer.*` — are stripped before
hashing so the hash isn't perturbed by our own bookkeeping.
On subsequent `Up` calls, an existing container with a mismatched
or missing hash is stopped, removed, and recreated from the
current spec; matching hashes preserve the container as before.
`Options.NoRecreate = true` falls back to the prior "always
reuse" behavior. `UpResult.RecreatedThisCall` lists which services
were destroyed and rebuilt this call.

## Status

The two README-tracked gaps from the previous iteration are closed:

- **Compose-mode `depends_on` health-gating** is wired through the
  compose host. Before bringing each service up, `Up` waits for every
  required dependency to satisfy its declared condition —
  `service_healthy` polls the dependency's healthcheck status,
  `service_completed_successfully` polls until the container has
  exited with code 0, `service_started` is implicit (the dep was
  already started by an earlier topological visit). The wait timeout
  per dependency is tunable via `Options.DependencyTimeout` (default
  5m, 500ms poll interval). The `com.docker.compose.depends_on`
  label encodes the actual condition rather than always reporting
  `service_started`.
- **VS Code remote-attach against compose-mode primaries** works the
  same way it does for single-container devcontainers. The
  `devcontainer.local_folder` and `devcontainer.config_file` labels
  are stamped only on the primary service's container — matching the
  upstream `devcontainers/cli` — so editor extensions and the
  upstream CLI find a unique container per workspace. Embedders that
  want the same one-call lookup the docker host exposes can use the
  new `(*Host).InspectPrimary(ctx, localWorkspace)` to retrieve just
  the primary's container info regardless of how many sidecars the
  project declares.

Two more compose-mode gaps are now closed:

- **Compose-side `build:`** is honored for both the primary service
  and sidecars. `BuildPrimaryImage` runs the primary's `build:` and
  uses the resulting tag as the base for feature derivation; `Up`
  builds any sidecar with a `build:` block through `pkg/buildx`
  before creating its container. `Options.SkipServiceBuild` lets
  embedders that manage their own image pipeline opt out.
- **Recreate-on-config-change** stamps a
  `com.docker.compose.config-hash` label on every container and
  rebuilds it on the next `Up` if the hash drifts (or is missing).
  `Options.NoRecreate` falls back to the prior "always reuse"
  behavior. `UpResult.RecreatedThisCall` lists which services were
  rebuilt.

The remaining compose-mode scope cuts (profiles/watch/develop/configs/secrets, `forwardPorts`/`appPort` overlay, tmpfs/image/cluster volume types) are listed under "Compose-mode scope cuts" above.

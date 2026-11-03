---
title: Go libraries
order: 5
product: dcb
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
| `pkg/templates` | Reads metadata annotations from published Template manifests. |
| `pkg/features` | Resolves Features and their install order, packages local Features, generates their documentation, and reports dependency graphs. |
| `pkg/substitute` | Substitutes configuration variables. |
| `pkg/workspace` | Places a workspace folder in a container: its Git root, a worktree's common Git folder, and the workspace folder. |
| `pkg/registryauth` | Reads registry credentials from the Docker client configuration, or holds credentials that the caller supplies. |

Package documentation is on
[pkg.go.dev](https://pkg.go.dev/github.com/wyrd-company/devcontainer-builder).

`spec.Load` and `DevContainer.Validate` accept common-only configurations.
`DevContainer.ValidateSource` checks the additional source requirement for build
and up. `ValidateEnvironment` also checks values in specs constructed in Go.
For empty Compose file arrays, `dockercompose.ResolveComposeFiles` and
`LoadProject` use the supplied workspace and `LocalEnv` for discovery.
`Project.ComposeFiles` contains the resolved file paths used in runtime labels.

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

New containers attach before start. `Up` returns after start and lifecycle work,
without waiting for container exit. Container output uses `Options.Stdout` and
`Options.Stderr` until EOF or cancellation of the `Up` context. Nil writers
discard output. Writers must support concurrent container and lifecycle output.
`Options.Stdin` supplies input when stdin is attached; nil sends no input. The
reader owner must release a blocking reader on cancellation. The Engine client
does not forward host process signals. `runArgs --cidfile` writes on the client
filesystem, with the same reservation behavior on ordinary and umask creates.

Docker `runArgs --umask` needs `docker.Options.NewCreateClient`, a factory for
an owned `github.com/moby/moby/client.Client`. Configure it for the same daemon,
TLS, headers and API policy as the legacy client. The host closes each returned
client after creation and umask confirmation. The CLI supplies this factory.
Other creation calls use the supplied legacy client. A library call with umask
and no factory returns an explicit error.

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
`core.trustctime`, `core.quotepath`, `core.ignorestat`, `core.sparseCheckout`,
`core.sparseCheckoutCone`, `core.protectHFS`, `core.protectNTFS`,
`core.precomposeUnicode`, `core.fsyncObjectFiles`, `core.safecrlf`,
`core.autocrlf`, `core.lockfilePid`, `sparse.expectFilesOutsideOfPatterns`,
`pager.color`, `color.pager`, `user.useConfigOnly`, `advice.detachedHead` or
`advice.statusHints` boolean gives no Git root, so `Resolve` mounts the
requested folder.
`core.safecrlf` also accepts `warn`, without regard to case.
`core.autocrlf` also accepts `input`, without regard to case.
Native discovery follows current Git for `core.lockfilePid`; Git before 2.54
ignores that key.
See [Workspace mounts](/docs/dcb/usage#workspace-mounts-and-container-selection).

## Generate Feature documentation

```go
err := features.GenerateDocumentation(ctx, features.DocumentationOptions{
    ProjectFolder: "./src",
    Registry:      "ghcr.io",
    Namespace:     "example/collection",
    GitHubOwner:   "example-owner",
    GitHubRepo:    "example-repo",
})
```

Pass the selected folder and publication strings directly; the CLI supplies its
`.` and `ghcr.io` defaults. `Logger` is optional and receives progress plus
`warning:` and `error:` diagnostics. Each visible child with an ID gets a README
from its metadata and optional NOTES.md. Missing metadata or an ID is skipped;
other entry failures return an error after the remaining entries are processed.
Metadata and notes stay unchanged; existing README files are replaced. The reader
retains upstream UTF-8 replacement, tolerant number recovery, and JavaScript
property/prototype conversion, including UTF-16 string property indices.
Optional `RegistryValue`, `NamespaceValue`, `GitHubOwnerValue`, and
`GitHubRepoValue` override their corresponding strings with a `DocumentationValue`.
Use `Text` for a string, a non-nil `Properties` map for an object with own
properties, or a non-nil `Elements` slice for an array. Objects take precedence
over arrays, and arrays over text. Values convert when rendering uses them.
An own `toString` property can fail conversion. Skipped entries and an empty
collection do not convert publication values. Owner and repository values are
used only when both differ from the empty string; arrays and objects differ
from it even when their text conversion is empty.
Conversion or map errors preserve that entry's
README and return an operation error after sibling processing. The
[usage guide](usage.md#generate-feature-documentation) describes content,
symlinks, tolerant JSONC parsing, and partial results.

## Package local Features

`features.Package(ctx, features.PackageOptions{Target: "./src", OutputDir: "./output"})`
writes Feature archives and `devcontainer-collection.json`. Empty paths use the
current folder and `./output`. `ForceCleanOutputDir` permits removal of an
existing output directory. The returned `*features.Collection` contains raw
Feature metadata, including unknown properties. Sources stay unchanged.

The operation returns an error for an invalid source, existing output without
force, missing required metadata, or a collection entry without `install.sh`.
An ID that would write outside the output directory is also rejected.
It can leave earlier archives on failure. It closes files and removes staging
before returning. Cancellation uses the supplied context. See
`TestPackageArchiveCanBeResolved` for packaging followed by archive resolution,
and [Usage](/docs/dcb/usage#package-local-features) for archive naming and flags.

## Report Feature versions

`features.ReadVersionConfig(workspace, configPath)` discovers or reads a JSONC
reporting configuration without requiring build fields. It reuses the collection
reader's JavaScript-compatible JSONC recovery and object enumeration.
Empty `configPath` uses
workspace discovery. `features.LoadVersionInfo(client, config)` returns a
`VersionReport` with `Features` and configuration `Order`. Its JSON marshaler
preserves this order and omits unavailable fields. A nil client uses the existing
registry credentials. Pass an `oci.Client` for a custom HTTP transport or source.
`oci.Client.PublishedTags` retrieves unmodified registry tags;
`features.StrictSortedVersions` filters and sorts full semver tags.

Reporting does not install Features, resolve dependencies, or write lockfiles.
Tag discovery failures produce unavailable fields; malformed lockfiles, digest
mismatches, layer errors, and invalid metadata versions return errors. Digest
metadata comes from the annotation or the first Feature layer. See
[version reporting](usage.md#report-feature-versions) for current/wanted/latest.

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

Image-source projection defaults `overrideCommand` to true. The shell wrapper
runs Feature entrypoints in order. With `overrideCommand: false`, pass the built
image's native OCI `ImageConfig` in `ProjectRequest.ImageConfig` or
`pkg/host/kubernetes.UpRequest.ImageConfig`. Its Entrypoint and Cmd run after
the Feature entrypoints. A missing image configuration returns an error; an
explicit empty configuration represents an image with no command. Image
environment stays inherited from the image. Feature environment overlays it in
Feature order, then configuration `containerEnv` wins, including empty values.
Pod values escape dollars so Kubernetes expansion preserves runtime literals.
Feature Dockerfile expansion occurs at build time.
These request fields are library inputs, separate from devcontainer.json.

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

## Read published Template metadata

`templates.Metadata(client, "registry.example.test/collection/sample:1")` returns
JSON from a Template manifest annotation with upstream number rounding and
object property semantics. Pass a nil OCI client to use Docker
registry credentials, or supply an `oci.Client` with an HTTP client and credential
source. The operation reads the manifest without fetching layers. Digest
references verify the manifest bytes. Missing or malformed metadata returns an
error.

`features.Info(client, mode, identifier, format)` returns a published Feature
report as text or JSON. A nil client uses Docker registry credentials.
`features.ErrInfoUnavailable` accompanies upstream failure output for missing
manifests or tags. Dependency reports reuse the registry and graph comparison
code without installing Features.

Feature information reports preserve escaped string content. Dependency graph node
identifiers use JavaScript JSON string and number serialization.

## Report configuration Feature dependencies

`features.ResolveDependencies(client, workspace)` returns the Mermaid graph and
ordered, pretty JSON installation report. Pass `nil` for the existing OCI
credential integration and an empty workspace for the current directory.
The workflow discovers the configuration, substitutes option values, reads
lockfile integrity pins and resolves local, HTTPS and OCI Feature metadata.
It computes hard and soft dependencies, deduplicates equivalent Features and
applies installation priorities. It does not install Features or write lockfiles.
On an ordering failure, the returned string contains the graph and the error
reports that no installation order exists. A configuration or resolution error
returns before the graph. See [usage](usage.md#resolve-feature-dependencies).

An HTTPS metadata retrieval failure retains the unexpanded archive entry;
the report does not install or admit its archive contents.

## Upgrade Feature lockfiles

`resolver.Upgrade(ctx, features.UpgradeOptions{Workspace: folder, DryRun: true})`
returns pretty lockfile JSON. `features.ValidateUpgradeOptions` validates the
paired Feature and target-version flags. `ConfigPath` selects an explicit
configuration. `Feature` and `TargetVersion` update a requirement before
resolution, including in dry-run mode. Existing lockfile pins remain in use;
dry-run can refresh an existing lockfile. Resolution uses the resolver's native
OCI client and does not build or install Features.

Dockerfile builds accept `build.options --network` with default, none or host.
The builder maps these to `DockerfileBuild.NetworkMode`; standalone and nested
Dockerfile solves request the host entitlement only for host mode. Build flags
not documented as supported in
[Configuration](configuration.md) return an explicit unsupported-option error.

`build.options --label` supplies final image labels with native buildx array
parsing and last-key precedence. Bare keys use empty values. Explicit
`builder.Options.Labels` override configuration labels.

Build options accept repeated `--build-arg KEY=VALUE` and `--build-arg=KEY=VALUE`.
Arguments override `build.args`; explicit library build arguments apply last.
Bare keys inherit the host environment when set. Unset bare keys leave earlier
values intact; explicit empty values remain empty. Empty keys are invalid.
Metadata and image user inference use `build.args` independently of these options,
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
uses the process environment); explicit empty maps disable inheritance. Final unset
keys require `ImageConfig`; removing an image-owned variable returns an error.
Merged security flags follow `runArgs`; merged security options use first-seen
union order. Detach conflicts with inherited output
attachment, and enabled init has no PodSpec equivalent. Other Docker flag and
remaining Compose translations remain separate coverage.

Kubernetes `ProjectCompose` primary command projection defaults `overrideCommand`
to false. Its shell wrapper runs Feature entrypoints in order, then the service
entrypoint and command. Absent service values fall back to the image; an explicit
empty entrypoint suppresses both image Entrypoint and Cmd, and an explicit empty
command suppresses image Cmd. Configuration `containerEnv` overrides ordered
Features, then service environment, above inherited image environment. Dollars
remain literal. Full Compose `runArgs` and native host Compose command
overrides need
separate checks.

Pass the selected image's native OCI configuration as
`ComposeProjectRequest.PrimaryImageConfig` when image command fallback is needed.
A nil value returns a contextual error; an explicit empty `oci.ImageConfig`
represents an image with no command. `overrideCommand: true` needs no image
configuration for commands. Explicit service entrypoints also need no image
command fallback. A final nil service environment value requires image
configuration so projection can determine whether omission is sufficient.
Removing an image-owned variable returns an error; a Feature or configuration
overlay can replace the nil value. These are library inputs, not schema fields.

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

Image-source Docker containers always run the pinned shell wrapper, including
`overrideCommand: false` without Features. That mode appends the inspected image
Entrypoint and Cmd. The image stop signal remains inherited unless `runArgs`
overrides it. Authored `--entrypoint` values are parsed and replaced by the
wrapper on Docker and Kubernetes. Docker `--rm` uses native Engine automatic
deletion. Kubernetes reports enabled `--rm` as unsupported because a standalone
Pod has no equivalent deletion behavior; a final `--rm=false` is accepted.

Build options accept `build.options --no-cache` and `build.options --pull`
with native Docker boolean assignments and last-value precedence. Dockerfile
invocation no-cache enables both cache bypass and image pulling before these
options apply. Explicit false values can override those generated flags. Cache
import generation still uses invocation no-cache; option-only cache bypass
retains configured imports. These settings reach standalone and nested
Dockerfile solves.

Build and Generate use `builder.Options.LocalEnv` for configuration variables.
Pass a selected map; nil supplies an empty environment. The library does not
read the process environment. `Workspace` supplies the local workspace path.
`Substitution` supplies the shared container workspace and identity context.
The input configuration is retained unchanged. The CLI supplies its environment.

Build inputs resolve local environment and workspace variables before the build.
This includes image names, Dockerfile and context paths, build arguments, target,
cache sources, build options, and feature option values. Build-time environment
and feature users also resolve configuration variables. Configuration keys stay
literal. Explicit command-line or library build arguments apply last and stay
literal. Runtime contributions keep their authored values for the host phase.

## Create without starting

Set `docker.UpRequest.NoStart` or `dockercompose.UpRequest.NoStart` when calling
`Host.Up`. Build the image through `pkg/builder` (or Compose's
`BuildPrimaryImage`) as for ordinary `Up`. The result retains the container ID
and creation fields; the start and lifecycle fields remain empty or false.
Call `Up` again with `NoStart: false` to start the same container and run setup
and lifecycle commands. Host initialization remains an explicit call to
`pkg/host/local.Initialize` for each invocation. See
[Create before first start](usage.md#create-before-first-start) for Compose,
bind-source, SELinux relabel limits, and repeat behavior.

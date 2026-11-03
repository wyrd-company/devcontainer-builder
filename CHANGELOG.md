# Changelog

## 0.3.0

### Breaking

- Expand native command and configuration compatibility, including create-only
  container provisioning through the CLI and Go library.

### Features

- Generate local Feature READMEs with `dcb features generate-docs` and the
  `features.GenerateDocumentation` library API, including options, notes,
  customizations, publication references, and deprecation notices. Typed publication
  values retain object properties and repeated values until rendering uses them.
- Query published Feature manifests, tags, and Mermaid dependency graphs with
  `dcb features info` and the `features.Info` library API.
  
  Report configuration Feature dependencies with `dcb features resolve-dependencies`
  and `features.ResolveDependencies`, including local and HTTPS Features, OCI
  lockfile pins, dependency graphs, and ordered installation options.
  
  Generate Feature lockfiles and update requirements with `dcb upgrade` and
  `features.Resolver.Upgrade`, including dry-run output and existing integrity pins.
- Package local Features and collections with `dcb features package` and the
  `features.Package` library API, producing archives and collection metadata.
- `dcb up`, `exec`, `read-configuration` and `run-user-commands` accept
  `--git-prefix`, and `workspace.Options.GitPrefix` takes the same value: the
  prefix of the host Git's build. A Git `include.path` of `%(prefix)/` followed
  by a relative path is then read below that prefix, as Git reads it, so a file
  there that Git rejects gives no workspace Git root, and the workspace folder is
  mounted. Without the setting, such an include is not read, as if the included
  file were missing. `workspace.Options.GitRoot` finds the Git root with that
  prefix.
- Report current, wanted, and latest OCI Feature versions with `dcb outdated`
  and the `features.LoadVersionInfo` library API, including lockfiles, digest
  metadata fallback, ordered JSON, and a text table.
- Generate local Template READMEs with `dcb templates generate-docs` and
  `templates.GenerateDocumentation`, including options, notes and metadata links.
- Read published Template metadata with `dcb templates metadata` and the
  `templates.Metadata` library API, including manifest digest verification.

### Fixes

- Honor an explicitly empty workspaceMount without adding a default workspace bind, while retaining additional mounts.
  
  Honor top-level Dockerfile build contexts. Reject object values for Compose file
  lists and build cache sources, and nested lifecycle command objects, which are
  not upstream configuration forms.
  
  Preserve string appPort publishing and Docker mount options across workspace,
  additional mounts, and runArgs, including CSV quoting and volume defaults.
  
  Preserve Docker runArgs boolean assignments, combined and attached short forms,
  stdin, TTY, automatic removal, and host environment inheritance. Accept overridden
  entrypoints as in the upstream CLI.
  
  Accept Docker environment and label files with native file precedence, and apply
  merged feature flags after runArgs.
  
  Accept Docker health and logging run arguments with native value precedence and
  conflict checks.
  
  Preserve exact Docker NanoCPU values and CPU period, quota, and realtime flags.
  
  Preserve Docker swappiness and block I/O weight, native integer forms for CPU
  shares, PID limits and OOM scores, and native OOM disable boolean spellings.
  
  Preserve Docker per-device block I/O weights and byte/IOPS throttle lists,
  including native validation, size units, repeated devices, and zero values.
  
  Preserve Docker storage options and Windows CPU and system-drive I/O creation
  controls with native value parsing and final-value precedence.
  
  Preserve native Docker network attachments, endpoint addresses, aliases, MAC
  addresses, and per-network options through Engine container creation.
  
  Preserve Docker umask through native creation and daemon confirmation; validate
  the deprecated kernel-memory flag without applying it.
  
  Honor upstream Docker stdout/stderr attachment, native stream validation and
  interactive stdin, with detach conflict checks. Reserve and write cidfile paths
  on the client before start, retaining written IDs and removing failed reservations.
  
  Accept common-only configuration reads while requiring sources for build and up.
  Discover Compose files for empty file arrays from the caller's environment and
  workspace. Validate environment value types and preserve upstream remote null
  behavior in exec and lifecycle commands.
  
  Map build.options --network to native BuildKit Dockerfile networking and request
  host entitlement for explicit host mode. Preserve final-value precedence and
  report unmapped build flags explicitly.
  
  Map build.options image labels to final native BuildKit image configuration
  with native array grammar, empty-key validation and last-key precedence.
  
  Build scratch-only Dockerfiles whose frontend result has no filesystem reference
  while retaining image configuration and normal nonempty filesystem builds.
  
  Map build.options build arguments to native Dockerfile solves with host inheritance,
  ordered precedence and independent config-only metadata and user inference.
  
  Map build.options target selection to authored and generated stages while retaining
  independent configured-target metadata and user inference.
  
  Map build cache options through native buildx parsing and BuildKit cache sessions,
  including inline cache, local export/import, ordering and contextual errors.
  
  Map build.options secret and SSH sources through native buildx parsing and
  BuildKit session providers for Dockerfile and generated feature builds.
  
  Execute initializeCommand on the host before every up, with upstream workspace,
  environment, command forms, parallel execution and failure semantics.
  
  Map Kubernetes image-source command overrides through native OCI image
  configuration and preserve Feature/configuration environment precedence.
  
  Preserve Kubernetes image-source runArgs booleans, short forms, stdin, TTY,
  host environment inheritance and merged security flag precedence.
  
  Preserve Kubernetes Compose primary service command defaults, explicit empty
  commands, image fallback, Feature entrypoints, literal dollars and configuration
  environment precedence. Report missing image configuration and unrepresentable
  image environment removals with primary service context.
  
  Preserve Kubernetes Compose sidecar empty command overrides and literal dollars.
  Use native image configuration for empty-command fallback and final environment
  unset checks, with contextual errors for unrepresentable image-owned removals.
  Normalize the native single-empty-string sidecar entrypoint reset.
  
  Preserve native Docker Compose command defaults, empty command overrides, image
  fallback, ordered Feature entrypoints and literal dollars. Keep remoteEnv
  exec-only, preserve environment precedence and remove unset image variables.
  Do not add stdin or TTY with the primary wrapper.
  
  Preserve the unconditional image-source wrapper and native image stop signal.
  Translate overridden Kubernetes entrypoints and report unsupported Pod automatic
  deletion instead of silently ignoring enabled runArgs --rm.
  
  Map build.options shared memory size and per-name resource limits to native
  BuildKit Dockerfile frontend settings with Docker parsing and precedence.
  
  Preserve native no-cache and pull build option booleans and generated flag
  precedence in both Dockerfile solve routes.
  
  Preserve native cgroup-parent build option strings, final value precedence and
  empty reset across standalone and nested Dockerfile solves.
  
  Preserve native extra host build options, CSV parsing, ordered IP mappings and
  Docker worker host gateway resolution on both Dockerfile solve routes.
  
  Resolve local environment and workspace variables in pre-container build inputs;
  let library callers select the environment used for build argument substitution.
  
  Create Docker and Compose containers without starting them through `up --no-start`
  and the Go host request NoStart field, then start them with ordinary up.
- Workspace Git discovery rejects invalid `core.autocrlf` values in its full
  config read, including file includes and command-line config. Boolean values
  and `input` remain accepted.
- Use the workspace folder as the Git root inside a bare repository or a .git folder.
- Honor GIT_CEILING_DIRECTORIES when finding the workspace Git root without running git.
- Workspace Git discovery reads command-line configuration from
  `GIT_CONFIG_COUNT`, its `GIT_CONFIG_KEY_n`/`GIT_CONFIG_VALUE_n` pairs, and
  `GIT_CONFIG_PARAMETERS`. Invalid syntax or a rejected `include.path` gives
  no Git root, so the workspace folder is mounted, as in the upstream CLI.
- Validate Git common-directory pointers before selecting the workspace Git root.
- Honor GIT_COMMON_DIR when selecting the workspace Git root.
- Find no workspace Git root when Git rejects a config file that another config
  file includes with `include.path`, such as an included file with a syntax
  error, an unreadable included file or an include cycle, as Git and the
  upstream CLI do, so that the workspace folder is mounted instead of a parent
  repository. As Git does, the repository config is read again after setup,
  with its includes, a `%(prefix)/` include followed by an absolute path names
  that path, and a relative config file or include name is looked up from the
  folder that the read starts at, without the length limit of that folder's path
  and with only search permission on that folder.
  Linux release builds look up accounts in `/etc/passwd` only and skip a
  `~name/` include whose account is not there, so that a Git configuration that
  works for an account of another account service finds the same Git root.
- Apply Git's core.worktree setting when selecting the workspace Git root. The
  Git root is found with the credentials of the `dcb` process, which can differ
  from the credentials of a `git` that the upstream CLI starts.
- Workspace Git discovery rejects invalid `advice.detachedHead` Boolean values
  in its full config read, including includes and command-line config.
- Use the workspace folder as the default Git root when GIT_DIR explicitly selects a valid repository.
- Stop at a file system boundary when finding the workspace Git root, unless
  `GIT_DISCOVERY_ACROSS_FILESYSTEM` is true, as Git does, so that a repository on
  another file system above the workspace is not mounted.
- Find the workspace Git root from the physical parent folders of a workspace
  folder reached through a symbolic link, as Git does, and keep the workspace
  folder's path for the root, as the upstream CLI does, so that the same
  repository is mounted.
- Workspace Git discovery rejects invalid `core.filemode` boolean values in
  its full config read, including file includes and command-line config.
- Workspace Git discovery rejects invalid `core.fsyncObjectFiles` boolean values
  in its full config read, including file includes and command-line config.
- Find no workspace Git root when Git rejects one of its global config files,
  such as a `~/.gitconfig` or `GIT_CONFIG_GLOBAL` file with a syntax error, as
  Git and the upstream CLI do, so that the workspace folder is mounted instead
  of a parent repository. Relative paths are read again from Git's working
  folder after setup, as Git does.
- Match Git HEAD syntax when selecting the workspace Git root.
- Workspace Git discovery rejects invalid `core.ignorecase` boolean values in
  its full config read, including file includes and command-line config.
- Workspace Git discovery rejects invalid `core.ignorestat` boolean values in
  its full config read, including file includes and command-line config.
- Workspace Git discovery rejects invalid `core.lockfilePid` Boolean values
  in its full config read, including file includes and command-line config.
- Honor GIT_OBJECT_DIRECTORY when selecting the workspace Git root.
- Workspace Git discovery rejects invalid `pager.color` and `color.pager` Boolean
  values in its full config read, including includes and command-line config.
- Workspace Git discovery rejects invalid `core.precomposeUnicode` boolean values
  in its full config read, including file includes and command-line config.
- Workspace Git discovery rejects invalid `core.protectHFS` boolean values in its
  full config read, including file includes and command-line config.
- Workspace Git discovery rejects invalid `core.protectNTFS` boolean values in its
  full config read, including file includes and command-line config.
- Workspace Git discovery rejects invalid `core.quotepath` boolean values in
  its full config read, including file includes and command-line config.
- Workspace Git discovery reads repository `config` and active
  `config.worktree` files as streams, including FIFOs, in both the repository
  format check and the full read after setup. Invalid syntax or typed values
  stop the read, as in Git.
- Apply the repository extension rules of Git 2.55 built without Rust when finding the workspace Git root without running git.
- Apply Git's repository format check and core.bare when selecting the workspace Git root.
- Workspace Git discovery rejects invalid `core.safecrlf` values in its full
  config read, including file includes and command-line config. Boolean values
  and `warn` remain accepted.
- Workspace Git discovery rejects invalid `sparse.expectFilesOutsideOfPatterns`
  Boolean values in its full config read, including includes and command-line
  config.
- Workspace Git discovery rejects invalid `core.sparseCheckout` boolean values
  in its full config read, including file includes and command-line config.
- Workspace Git discovery rejects invalid `core.sparseCheckoutCone` boolean values
  in its full config read, including file includes and command-line config.
- Workspace Git discovery rejects invalid `advice.statusHints` Boolean values
  in its full config read, including includes and command-line config.
- Workspace Git discovery rejects invalid `core.symlinks` boolean values in
  its full config read, including file includes and command-line config.
- Find no workspace Git root when Git rejects the system config file that
  `GIT_CONFIG_SYSTEM` names, or a `GIT_CONFIG_NOSYSTEM` value, as Git and the
  upstream CLI do, so that the workspace folder is mounted instead of a parent
  repository. A system or global config file that is a FIFO is read as Git
  reads it, until its writer closes it, instead of giving no Git root.
- Workspace Git discovery rejects invalid `core.trustctime` boolean values in
  its full config read, including file includes and command-line config.
- Find no workspace Git root when the workspace folder or a parent folder holds
  a `.git` entry that `stat` fails on for a reason other than a missing entry, or
  a gitfile that Git cannot read, such as a symbolic link loop or a gitfile that
  cannot be opened, as Git and the upstream CLI do, so that the workspace folder
  is mounted instead of a parent repository or a failed command.
- Workspace Git discovery rejects invalid `user.useConfigOnly` Boolean values
  in its full config read, including includes and command-line config.
- Honor GIT_WORK_TREE when selecting the workspace Git root without running git.
- Match Git's gitfile format and size limit when selecting the workspace Git root.
- Follow a relative gitfile target through the file system as Git does when
  selecting the workspace Git root, so that a symbolic link before `..` leads to
  the repository Git uses.
- Mount only the workspace folder when GIT_DIR names a missing repository or an invalid gitfile target.
- Reuse verified release artifacts for publication retries and wait for npm visibility without republishing accepted packages.
- Persist user environment probe results in the supplied container session data folder and reuse upstream-compatible mode-specific caches.

## 0.2.1

### Fixes

- Honor Docker container names supplied through runArgs, including reuse and explicit recreation.
- Honor read-only options in Docker mount object targets and reject non-spec mount object properties.

## 0.2.0

### Features

- Go library callers can supply one registry credential source for every registry request: `builder.Options.RegistryAuth` reaches BuildKit pulls and pushes, feature downloads and base image reads, and `dockercompose` forwards its sources to service builds. `registryauth.Static` holds caller credentials, and `registryauth.NewWithoutHelpers` reads the Docker client configuration without running credential helpers.

### Fixes

- A feature's `containerEnv` is now expanded as the reference CLI's Dockerfile `ENV` expands it, in the reference CLI's key order (array-index keys such as `1` first, then the others as declared). A value such as the Go feature's `PATH=/usr/local/go/bin:/go/bin:${PATH}` keeps the base image's `PATH`, so the feature installs and the image keeps a working `PATH`. The generated Dockerfile writes these values the same way.
- A credential source error that makes a build, a feature download or a base image read fail is now part of the returned error, so that `errors.Is` matches it.

## 0.1.0

### Features

- Initial dcb release with development-container image builds, container lifecycle commands, Compose support, and embeddable Go libraries.
  
  Lifecycle controls support waitFor stop points, prebuild reruns, personalization stops, and requiring an existing container. Successful run-user-commands results identify the lifecycle stop point.
  
  Configuration result controls include optional configuration, merged metadata, and resolved feature configuration. Read-configuration applies existing-container substitutions to every requested configuration result independently of output flags.
  
  Build output accepts buildx exporter specifications for filesystem, image archive, image-store, and registry exports through native APIs.
  
  GPU availability controls honor single-container and Compose host GPU requirements through Docker Engine runtime detection and device requests.
  
  Data-folder options select host feature/build staging, Compose config/secret materialization, and container lifecycle/dotfiles marker locations. Compose teardown discovers and removes materialized copies after restart without repeating the selected folder; cleanup errors remain retryable.
  
  Paired terminal dimension flags size lifecycle and exec pseudo-terminals in single-container and Compose mode.
  
  Lifecycle commands take a remote environment and a secrets file; dcb masks secret values in its output and does not store them.
  
  Workspace mounts follow upstream: the workspace's Git root is mounted by default, a linked worktree's common Git folder can be mounted, additional bind and volume mounts can be added, and id labels select the container.
  
  Feature lockfiles pin remote features and dependencies, verify integrity, and support frozen builds with atomic publication after success.
  
  Hidden upstream feature controls support legacy identifier mapping, feature customization persistence, configuration remoteEnv metadata omission, and Dockerfile syntax omission. RemoteEnv metadata persists by default.

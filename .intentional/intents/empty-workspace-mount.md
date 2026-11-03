---
dcb: patch
---

Honor an explicitly empty workspaceMount without adding a default workspace bind, while retaining additional mounts.

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

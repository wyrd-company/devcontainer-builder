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

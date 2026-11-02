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

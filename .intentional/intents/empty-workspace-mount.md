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

---
relationships:
  references: [taskfile, CONTRIBUTING]
---
# devcontainer-builder

Go libraries and the `dcb` command for development containers.

Use the verbs in `taskfile.yml`; `task check` is the pre-review gate.
`task --list-all` includes integration and reference maintenance commands.

Preserve upstream-compatible labels, lifecycle markers, and command semantics.
Reference repositories are read-only submodules. Do not edit them or update their
pinned commits as part of application changes. Generated build output and
`go.sum` are owned by their generating tools.

User documentation belongs in `docs/user/`, which wyrd.foo publishes from release
and `dcb-docs@*` tags (see CONTRIBUTING). Engineering
artifacts belong in the other typed subdirectories of `docs/`, which stay private.
Use README as the entry point.

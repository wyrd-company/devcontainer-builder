---
relationships:
  references: [upstream-options, ../../reference/spec/schemas/devContainer.base.schema.json, ../../reference/cli/src/spec-node/singleContainer.ts, ../user/configuration]
---

# Configuration schema conformance

The upstream Dev Container schemas define configuration forms. DCB-specific
configuration extensions are not supported. Reference submodules define the
comparison contract; their commits stay unchanged during application changes.

Dockerfile paths and contexts support both upstream forms: `build.dockerfile`
with `build.context`, and `dockerFile` with top-level `context`. A top-level
Dockerfile can carry `build` options. File lists and cache sources accept strings
or string arrays. Parallel lifecycle commands contain string or string-array
values, without nested command objects.

## Audit work queue

Configuration conformance takes priority over additional command work. These
runtime consumers require pinned differential checks and corrections:

- String `appPort` values must retain Docker publishing syntax. Only numeric
  values receive the upstream loopback mapping.
- String mounts must retain Docker mount options across `workspaceMount`,
  `mounts`, and `runArgs --mount`.
- Docker `runArgs` must preserve container-affecting flags, boolean assignments,
  attached short options, and environment inheritance.
- Empty Compose file arrays require upstream Compose file discovery.
- Reading a common-only document must not require a build/run source.
- Environment values require schema type checks and pinned null-value behavior.
- `build.options` needs a native BuildKit mapping; nonempty values are rejected.
- `initializeCommand` is preserved but not executed. The public configuration
  guide describes this limitation.
- Kubernetes projection needs checks for command override, environment ordering,
  and its supported Docker argument translations.

Main integration requires a simulated rebase before any remote branch rewrite.
Each correction needs tests, valid guard mutations with green controls, a clean
full gate, and independent review. The command option inventory is a separate
measurement and does not establish configuration schema conformance.

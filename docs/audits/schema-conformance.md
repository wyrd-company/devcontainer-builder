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

Docker publishing preserves string `appPort` values. Numeric values map to the
same port on loopback. Docker mount strings use the native Docker CLI parser
across `workspaceMount`, `mounts`, object target options, and `runArgs --mount`.
The parser retains CSV quoting, volume defaults, bind, volume, image, and tmpfs
options. Invalid options return contextual errors. The split Moby mount type is
converted through its wire representation with unknown fields rejected so that
the Engine client cannot silently discard an option.

## Audit work queue

Configuration conformance takes priority over additional command work. These
runtime consumers require pinned differential checks and corrections:

- Docker `runArgs` needs remaining flag coverage, ordering, and precedence
  checks. Boolean assignments, combined short booleans, attached short values,
  stdin, TTY, removal, environment inheritance, and overridden entrypoints are
  supported.
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

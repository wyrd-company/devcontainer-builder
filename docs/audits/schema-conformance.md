---
relationships:
  references: [upstream-options,
  ../../reference/spec/schemas/devContainer.base.schema.json,
  ../../reference/cli/src/spec-node/singleContainer.ts, ../user/configuration]
---

# Configuration schema conformance

The upstream Dev Container schemas define configuration forms. DCB-specific
configuration extensions are not supported. Reference submodules define the
comparison contract; their commits stay unchanged during application changes.

Dockerfile paths and contexts support both upstream forms: `build.dockerfile`
with `build.context`, and `dockerFile` with top-level `context`. A top-level
Dockerfile can carry `build` options. File lists and cache sources accept
strings
or string arrays. Parallel lifecycle commands contain string or string-array
values, without nested command objects.

Docker publishing preserves string `appPort` values. Numeric values map to the
same port on loopback. Docker mount strings use the native Docker CLI parser
across `workspaceMount`, `mounts`, object target options, and `runArgs --mount`.
The parser retains CSV quoting, volume defaults, bind, volume, image, and tmpfs
options. Invalid options return contextual errors. The split Moby mount type is
converted through its wire representation with unknown fields rejected so that
the Engine client cannot silently discard an option.

Common-only documents are accepted by configuration reads. Build and up require
a source. Configuration and merged output preserve empty Compose declarations.
Empty Compose file arrays discover files from the supplied workspace
and environment: COMPOSE_FILE, literal workspace .env COMPOSE_FILE, then
legacy docker-compose.yml and its existing file override. Explicit paths remain
relative to the configuration directory. Resolved discovery paths reach runtime
labels. Environment schema validation accepts string containerEnv values and
string/null remoteEnv values. Remote null survives configuration merging and
becomes literal "null" for CLI and host exec/lifecycle consumers.

## Audit work queue

Configuration conformance takes priority over additional command work. These
runtime consumers require pinned differential checks and corrections:

- Docker `runArgs` needs remaining flag coverage, ordering, and precedence
  checks. Boolean assignments, combined short booleans, attached short values,
  stdin, TTY, removal, environment inheritance, and overridden entrypoints are
  supported. Native Docker environment and label files load before explicit
  flags. Merged init, privileged, capability and security option flags follow
  runArgs. Health and logging options retain native Docker values, final scalar
  precedence,
  health conflict checks and logging key precedence. Remaining Docker flag
  coverage stays queued. Umask uses native octal parsing, a configured native
  creation client and daemon confirmation before start. Deprecated kernel-memory
  uses native memory-size validation without applying a resource limit. Explicit
  attachment follows upstream's inherited stdout/stderr streams, native stream
  validation, interactive stdin union, and detach conflict. New containers
  attach
  before start; streams follow the caller context. Cidfile reserves a local path
  before creation, writes exact ID bytes before start, removes unwritten
  reservations, and retains written IDs on both creation routes. Network
  endpoint flags retain first-attachment legacy
  values, repeated native CSV attachments, aliases, IP addresses, MAC addresses,
  links, driver options, gateway priority, and conflict validation. Endpoint
  configuration reaches Engine creation; daemon enforcement stays daemon-owned.
  CPU period, quota, and realtime values use Docker integer
  parsing; NanoCPU values use the native exact rational parser. Swappiness and
  block I/O weight retain Docker integer forms and final scalar values. CPU
  shares, PID limits, and OOM score adjustments accept base-zero integers;
  OOM disable uses native boolean spellings. Per-device block I/O weight and
  byte/IOPS throttles use native validators and retain ordered repeated entries.
  Storage options retain last-value-per-key semantics. Windows CPU count, CPU
  percent, system-drive bandwidth and IOPS map to Engine creation fields with
  native integer and memory-size grammar. Platform enforcement is daemon-owned.
- `build.options --network` maps default/empty, none and host modes to the
  Dockerfile frontend, with final scalar precedence and host entitlement on
  both solve routes. Image labels map to the final image configuration. Build
  arguments map to native frontend arguments. Cache options use native buildx
  parsing and BuildKit sessions. Other build flags still
  need native mappings. The network
  comparison covers pinned argv and native BuildKit acceptance, not network
  traffic enforcement or full build flag coverage.
- `initializeCommand` runs natively on the host for every CLI up before build
  and container startup. The pinned entry point, workspace cwd, host
  environment,
  shell/argv/parallel forms, error and interrupt behavior define this mapping.
  This does not claim full schema validation or terminal emulation parity.
- Kubernetes image-source projection uses the pinned shell wrapper and Feature
  entrypoints. Explicit false command override requires native OCI image
  configuration and appends Entrypoint then Cmd. Configuration environment wins
  over ordered Features, above native image inheritance. Supported Docker
  argument translations still need checks.

Build options image labels use native buildx array parsing and last-key
precedence. Empty keys fail before staging. Final image configuration retains
base labels, applies configuration labels, then explicit library labels.
Secret and SSH options use native BuildKit session providers.

Dockerfile gateway solves use native LLB scratch state when the frontend returns
no filesystem reference. Scratch-only builds retain their image configuration;
nonempty frontend references and conversion errors retain their normal handling.
Standalone and nested local exports cover empty filesystems and COPY controls.

Main integration requires a simulated rebase before any remote branch rewrite.
Each correction needs tests, valid guard mutations with green controls, a clean
full gate, and independent review. The command option inventory is a separate
measurement and does not establish configuration schema conformance.

Build options accept repeated `--build-arg KEY=VALUE` and
`--build-arg=KEY=VALUE`.
Arguments override `build.args`; explicit library build arguments apply last.
Bare keys inherit the host environment when set. Unset bare keys leave earlier
values intact; explicit empty values remain empty. Empty keys are invalid.
Metadata and image user inference use `build.args` independently of these
options,
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
uses the process environment); explicit empty maps disable inheritance. Final
unset
keys require `ImageConfig`; removing an image-owned variable returns an error.
Merged security flags follow `runArgs`; merged security options use first-seen
union order. Detach conflicts with inherited output
attachment, and enabled init has no PodSpec equivalent. Other Docker flag and
remaining Compose translations remain separate coverage.

Kubernetes `ProjectCompose` primary command projection defaults
`overrideCommand`
to false. Its shell wrapper runs Feature entrypoints in order, then the service
entrypoint and command. Absent service values fall back to the image; an
explicit
empty entrypoint suppresses both image Entrypoint and Cmd, and an explicit empty
command suppresses image Cmd. Configuration `containerEnv` overrides ordered
Features, then service environment, above inherited image environment. Dollars
remain literal. Full Compose `runArgs` and native host Compose command
overrides need separate
checks.

Kubernetes Compose sidecars preserve service command and entrypoint defaults,
including explicit empty overrides. `ComposeProjectRequest.SidecarImageConfigs`
supplies native OCI image configuration by service name when an empty command
needs image entrypoint fallback or an unset environment key needs checking.
An explicit empty entrypoint suppresses image Entrypoint and Cmd; a service with
no executable returns a contextual error. Unset image-owned environment values
return an error because Pod environment omission inherits image values. Explicit
empty environment values remain empty. Authored command and environment dollars
remain literal. Sidecars receive no primary configuration or Feature overlays.

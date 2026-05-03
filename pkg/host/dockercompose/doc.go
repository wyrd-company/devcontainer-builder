// Package dockercompose brings a docker-compose-mode devcontainer up
// against a docker-compatible daemon. The package owns the full
// orchestration loop natively: it parses and merges compose files
// through compose-spec/compose-go, generates an in-memory override
// layer that injects the workspace mount + remoteEnv + the
// feature-derived image into the primary service, and creates
// networks / volumes / containers via the docker engine API. There is
// no shell-out to the `docker compose` CLI.
//
// The "primary service" is the one named in devcontainer.json's
// `service` field: it's where lifecycle commands run and where Exec
// attaches. Other services come up alongside it, ordered by their
// compose `depends_on` graph; `service_healthy` and
// `service_completed_successfully` conditions are honored by polling
// the dependency's container state before the dependent is created
// (timeout tunable via Options.DependencyTimeout).
//
// # Compatibility
//
// Containers, networks, and volumes are labeled with the standard
// compose keys (`com.docker.compose.project`, `com.docker.compose.service`,
// etc.). The devcontainer-discovery labels
// (`devcontainer.local_folder`, `devcontainer.config_file`) are
// stamped only onto the primary service's container, matching the
// upstream `devcontainers/cli` so editor extensions (and the
// InspectPrimary helper) find a unique target. A project brought up
// here can be torn down by `docker compose down -p <name>` and vice
// versa.
//
// # Compose-side build
//
// Both the primary and sidecars may declare a compose `build:` block.
// BuildPrimaryImage runs the primary's build through pkg/buildx and
// uses the result as the base for feature derivation; Up walks the
// project for non-primary services with a build block and compiles
// each via pkg/buildx before creating its container, then mutates the
// in-memory project so the spawn step uses the freshly-built tag.
// Options.SkipServiceBuild opts out for embedders that manage their
// own image pipeline. dockerfile_inline is rejected.
//
// # Recreate-on-config-change
//
// Every container created by Up carries a
// `com.docker.compose.config-hash` label computed from a normalized
// JSON snapshot of its compose ServiceConfig (image, entrypoint,
// command, env, volumes, ports, networks, depends_on, security
// flags, …) with host-managed labels stripped. On subsequent Up
// calls, an existing container whose hash disagrees with the current
// spec (or is missing) is stopped, removed, and rebuilt from the
// current spec; matching hashes preserve the container. Set
// Options.NoRecreate to fall back to the prior "always reuse"
// behavior; UpResult.RecreatedThisCall lists which services were
// destroyed and rebuilt this call.
//
// # Profiles
//
// Up takes a Profiles slice on its request. Compose services whose
// `profiles:` list is non-empty are excluded unless one of the
// activated profiles matches (or "*" is passed). The primary service
// stays enabled regardless — without it there's no devcontainer —
// and any service named in spec.RunServices stays enabled too,
// matching `docker compose up <svc>`'s "implicitly activate this
// service's profiles" behavior.
//
// # Configs and secrets
//
// Top-level compose `configs:` and `secrets:` are honored in
// standalone (non-swarm) mode. Each ServiceConfig.Configs /
// .Secrets reference is translated into a read-only bind mount of
// the source file into the container; default targets match
// upstream (`/<source>` for configs, `/run/secrets/<source>` for
// secrets) and explicit `target:` overrides land verbatim. Source
// resolution follows the spec:
//
//   - `file:` is bound directly.
//   - `environment:` reads the named host env var (from
//     UpRequest.LocalEnv) and writes it to a project-scoped temp
//     file under $TMPDIR/dcb-compose/<sha256(project)[:8]>/...
//     before binding it. Remove() cleans these up.
//   - `content:` writes the literal value to the same temp area.
//   - `external: true` is rejected — the package doesn't speak swarm.
//   - `driver:` is rejected — only file/env/content sources work.
//
// UID/GID/Mode on the FileReferenceConfig are not enforced: the bind
// mount inherits the on-host file's ownership (matching `docker
// compose` standalone mode).
//
// # Watch / develop
//
// Host.Watch loops over every service in the project that declares
// `develop.watch` and polls each trigger's Path for filesystem
// changes. Action coverage:
//
//   - `sync`: changed files are tarred and pushed via the
//     CopyToContainer engine API; deletions trigger an `rm -rf`
//     exec inside the target container.
//   - `restart`: ContainerRestart on the target.
//   - `sync+restart`: both.
//   - `sync+exec`: sync, then run trigger.exec.command via the
//     same Exec path lifecycle commands use.
//   - `rebuild`: not honored — would need the build pipeline. Any
//     active rebuild trigger errors out.
//
// Include/ignore use `*`, `?`, and `**` (leading `**/` / trailing
// `/**`) globs. Watch blocks until ctx is cancelled; the caller
// owns the goroutine.
//
// # Scope cuts
//
//   - tmpfs, image, and cluster volume types return an error rather
//     than being silently dropped.
//   - port publishing from compose `ports:` is forwarded to the engine,
//     but devcontainer.json `forwardPorts`/`appPort` are intentionally
//     not layered onto compose services (the user already has compose
//     `ports:` for that).
package dockercompose

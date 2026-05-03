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
// # Scope cuts in this iteration
//
//   - profiles, watch, configs, secrets, develop: not honored.
//   - tmpfs, image, and cluster volume types return an error rather
//     than being silently dropped.
//   - port publishing from compose `ports:` is forwarded to the engine,
//     but devcontainer.json `forwardPorts`/`appPort` are intentionally
//     not layered onto compose services (the user already has compose
//     `ports:` for that).
package dockercompose

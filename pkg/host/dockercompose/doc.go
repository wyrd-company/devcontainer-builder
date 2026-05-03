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
// # Scope cuts in this iteration
//
//   - profiles, watch, configs, secrets, develop: not honored.
//   - compose-side `build:` for non-primary services: not built; the
//     service must reference a pullable `image:`.
//   - recreate-on-config-change: containers are reused if present,
//     created otherwise — no diff/recreate logic.
//   - port publishing from compose `ports:` is forwarded to the engine,
//     but devcontainer.json `forwardPorts`/`appPort` are intentionally
//     not layered onto compose services (the user already has compose
//     `ports:` for that).
package dockercompose

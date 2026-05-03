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
// compose `depends_on` graph (start-only — health gating is not yet
// implemented; see scope notes in README).
//
// # Compatibility
//
// Containers, networks, and volumes are labeled with the standard
// compose keys (`com.docker.compose.project`, `com.docker.compose.service`,
// etc.) plus the devcontainer keys used by the single-container host
// (`devcontainer.local_folder`, `devcontainer.config_file`). A project
// brought up here can be torn down by `docker compose down -p <name>`
// and vice versa.
//
// # Scope cuts in this iteration
//
//   - depends_on conditions: ordering only; service_healthy /
//     service_completed_successfully are treated as service_started.
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

# Changelog

## 0.1.0

### Features

- Initial dcb release with development-container image builds, container lifecycle commands, Compose support, and embeddable Go libraries.
  
  Lifecycle controls support waitFor stop points, prebuild reruns, personalization stops, and requiring an existing container. Successful run-user-commands results identify the lifecycle stop point.
  
  Configuration result controls include optional configuration, merged metadata, and resolved feature configuration. Read-configuration applies existing-container substitutions to every requested configuration result independently of output flags.
  
  Build output accepts buildx exporter specifications for filesystem, image archive, image-store, and registry exports through native APIs.
  
  GPU availability controls honor single-container and Compose host GPU requirements through Docker Engine runtime detection and device requests.
  
  Data-folder options select host feature/build staging, Compose config/secret materialization, and container lifecycle/dotfiles marker locations. Compose teardown discovers and removes materialized copies after restart without repeating the selected folder; cleanup errors remain retryable.
  
  Paired terminal dimension flags size lifecycle and exec pseudo-terminals in single-container and Compose mode.
  
  Lifecycle commands take a remote environment and a secrets file; dcb masks secret values in its output and does not store them.
  
  Workspace mounts follow upstream: the workspace's Git root is mounted by default, a linked worktree's common Git folder can be mounted, additional bind and volume mounts can be added, and id labels select the container.
  
  Feature lockfiles pin remote features and dependencies, verify integrity, and support frozen builds with atomic publication after success.
  
  Hidden upstream feature controls support legacy identifier mapping, feature customization persistence, configuration remoteEnv metadata omission, and Dockerfile syntax omission. RemoteEnv metadata persists by default.

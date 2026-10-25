# Changelog

## 0.2.0

### Features

- Go library callers can supply one registry credential source for every registry request: `builder.Options.RegistryAuth` reaches BuildKit pulls and pushes, feature downloads and base image reads, and `dockercompose` forwards its sources to service builds. `registryauth.Static` holds caller credentials, and `registryauth.NewWithoutHelpers` reads the Docker client configuration without running credential helpers.

### Fixes

- A feature's `containerEnv` is now expanded as the reference CLI's Dockerfile `ENV` expands it, in the reference CLI's key order (array-index keys such as `1` first, then the others as declared). A value such as the Go feature's `PATH=/usr/local/go/bin:/go/bin:${PATH}` keeps the base image's `PATH`, so the feature installs and the image keeps a working `PATH`. The generated Dockerfile writes these values the same way.
- A credential source error that makes a build, a feature download or a base image read fail is now part of the returned error, so that `errors.Is` matches it.

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

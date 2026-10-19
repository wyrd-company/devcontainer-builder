---
dcb: minor
---

Initial dcb release with development-container image builds, container lifecycle commands, Compose support, and embeddable Go libraries.

Lifecycle controls support waitFor stop points, prebuild reruns, personalization stops, and requiring an existing container.

Configuration result controls include optional configuration, merged metadata, and resolved feature configuration.

Build output accepts buildx exporter specifications for filesystem, image archive, image-store, and registry exports through native APIs.

GPU availability controls honor single-container and Compose host GPU requirements through Docker Engine runtime detection and device requests.

Data-folder options select host feature/build staging and container lifecycle/dotfiles marker locations.

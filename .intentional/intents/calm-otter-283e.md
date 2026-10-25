---
dcb: patch
---

A feature's `containerEnv` is now expanded as the reference CLI's Dockerfile `ENV` expands it, in the reference CLI's key order (array-index keys such as `1` first, then the others as declared). A value such as the Go feature's `PATH=/usr/local/go/bin:/go/bin:${PATH}` keeps the base image's `PATH`, so the feature installs and the image keeps a working `PATH`. The generated Dockerfile writes these values the same way.

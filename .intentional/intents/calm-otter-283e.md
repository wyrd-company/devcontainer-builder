---
dcb: patch
---

A feature's `containerEnv` is now expanded as the reference CLI's Dockerfile `ENV` expands it, in the order the feature declares its keys. A value such as the Go feature's `PATH=/usr/local/go/bin:/go/bin:${PATH}` keeps the base image's `PATH`, so the feature installs and the image keeps a working `PATH`. The generated Dockerfile writes these values the same way.

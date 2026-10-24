---
dcb: patch
---

A credential source error that makes a build, a feature download or a base image read fail is now part of the returned error, so that `errors.Is` matches it.

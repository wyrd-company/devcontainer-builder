---
dcb: minor
---

Go library callers can supply one registry credential source for every registry request: `builder.Options.RegistryAuth` reaches BuildKit pulls and pushes, feature downloads and base image reads, and `dockercompose` forwards its sources to service builds. `registryauth.Static` holds caller credentials, and `registryauth.NewWithoutHelpers` reads the Docker client configuration without running credential helpers.

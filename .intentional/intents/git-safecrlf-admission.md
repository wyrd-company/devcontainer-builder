---
dcb: patch
---

Workspace Git discovery rejects invalid `core.safecrlf` values in its full
config read, including file includes and command-line config. Boolean values
and `warn` remain accepted.

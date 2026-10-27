---
dcb: patch
---

Workspace Git discovery rejects invalid `core.autocrlf` values in its full
config read, including file includes and command-line config. Boolean values
and `input` remain accepted.

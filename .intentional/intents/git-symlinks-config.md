---
dcb: patch
---

Workspace Git discovery rejects invalid `core.symlinks` boolean values in
its full config read, including file includes and command-line config.

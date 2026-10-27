---
dcb: patch
---

Workspace Git discovery rejects invalid `core.trustctime` boolean values in
its full config read, including file includes and command-line config.

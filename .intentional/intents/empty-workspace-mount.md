---
dcb: patch
---

Honor an explicitly empty workspaceMount without adding a default workspace bind, while retaining additional mounts.

Honor top-level Dockerfile build contexts. Reject object values for Compose file
lists and build cache sources, and nested lifecycle command objects, which are
not upstream configuration forms.

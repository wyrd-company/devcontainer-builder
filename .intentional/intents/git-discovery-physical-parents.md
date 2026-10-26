---
dcb: patch
---

Find the workspace Git root from the physical parent folders of a workspace
folder reached through a symbolic link, as Git does, and keep the workspace
folder's path for the root, as the upstream CLI does, so that the same
repository is mounted.

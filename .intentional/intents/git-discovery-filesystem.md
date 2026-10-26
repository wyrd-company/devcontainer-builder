---
dcb: patch
---

Stop at a file system boundary when finding the workspace Git root, unless
`GIT_DISCOVERY_ACROSS_FILESYSTEM` is true, as Git does, so that a repository on
another file system above the workspace is not mounted.

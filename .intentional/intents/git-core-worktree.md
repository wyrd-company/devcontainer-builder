---
dcb: patch
---

Apply Git's core.worktree setting when selecting the workspace Git root. The
Git root is found with the credentials of the `dcb` process, which can differ
from the credentials of a `git` that the upstream CLI starts.

---
dcb: patch
---

Workspace Git discovery reads repository `config` and active
`config.worktree` files as streams, including FIFOs, in both the repository
format check and the full read after setup. Invalid syntax or typed values
stop the read, as in Git.

---
dcb: minor
---

`dcb up`, `exec`, `read-configuration` and `run-user-commands` accept
`--git-prefix`, and `workspace.Options.GitPrefix` takes the same value: the
prefix of the host Git's build. A Git `include.path` of `%(prefix)/` followed
by a relative path is then read below that prefix, as Git reads it, so a file
there that Git rejects gives no workspace Git root, and the workspace folder is
mounted. Without the setting, such an include is not read, as if the included
file were missing. `workspace.Options.GitRoot` finds the Git root with that
prefix.

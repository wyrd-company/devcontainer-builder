---
dcb: patch
---

Find no workspace Git root when Git rejects one of its global config files,
such as a `~/.gitconfig` or `GIT_CONFIG_GLOBAL` file with a syntax error, as
Git and the upstream CLI do, so that the workspace folder is mounted instead
of a parent repository.

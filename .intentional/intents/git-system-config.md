---
dcb: patch
---

Find no workspace Git root when Git rejects the system config file that
`GIT_CONFIG_SYSTEM` names, or a `GIT_CONFIG_NOSYSTEM` value, as Git and the
upstream CLI do, so that the workspace folder is mounted instead of a parent
repository. A system or global config file that is a FIFO is read as Git
reads it, until its writer closes it, instead of giving no Git root.

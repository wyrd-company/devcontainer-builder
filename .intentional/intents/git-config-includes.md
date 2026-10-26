---
dcb: patch
---

Find no workspace Git root when Git rejects a config file that another config
file includes with `include.path`, such as an included file with a syntax
error, an unreadable included file or an include cycle, as Git and the
upstream CLI do, so that the workspace folder is mounted instead of a parent
repository. As Git does, the repository config is read again after setup,
with its includes.

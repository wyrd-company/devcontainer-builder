---
dcb: patch
---

Find no workspace Git root when the workspace folder or a parent folder holds
a `.git` entry that `stat` fails on for a reason other than a missing entry, or
a gitfile that Git cannot read, such as a symbolic link loop or a gitfile that
cannot be opened, as Git and the upstream CLI do, so that the workspace folder
is mounted instead of a parent repository or a failed command.

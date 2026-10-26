---
dcb: patch
---

Follow a relative gitfile target through the file system as Git does when
selecting the workspace Git root, so that a symbolic link before `..` leads to
the repository Git uses.

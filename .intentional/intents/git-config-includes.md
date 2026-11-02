---
dcb: patch
---

Find no workspace Git root when Git rejects a config file that another config
file includes with `include.path`, such as an included file with a syntax
error, an unreadable included file or an include cycle, as Git and the
upstream CLI do, so that the workspace folder is mounted instead of a parent
repository. As Git does, the repository config is read again after setup,
with its includes, a `%(prefix)/` include followed by an absolute path names
that path, and a relative config file or include name is looked up from the
folder that the read starts at, without the length limit of that folder's path
and with only search permission on that folder.
Linux release builds look up accounts in `/etc/passwd` only and skip a
`~name/` include whose account is not there, so that a Git configuration that
works for an account of another account service finds the same Git root.

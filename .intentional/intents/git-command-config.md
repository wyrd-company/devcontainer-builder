---
dcb: patch
---

Workspace Git discovery reads command-line configuration from
`GIT_CONFIG_COUNT`, its `GIT_CONFIG_KEY_n`/`GIT_CONFIG_VALUE_n` pairs, and
`GIT_CONFIG_PARAMETERS`. Invalid syntax or a rejected `include.path` gives
no Git root, so the workspace folder is mounted, as in the upstream CLI.

---
relationships:
  references: [taskfile, AGENTS]
---

# Contributing

Propose a focused change through a pull request. Discuss changes to supported
commands or upstream compatibility before implementation. Use a conventional
commit title and explain the resulting behavior and validation.

Use the repository devcontainer for Go, Docker, Task, Intentional, Actionlint,
and Lefthook. Run `task check` before review. Docker integration checks require
a working Docker daemon; Kubernetes validation uses kubeconform without a cluster.
Optional upstream fixture tests run when the reference submodules are initialized.
The Git account-lookup test builds a C fixture and runs when a C compiler is installed.
`task parity` compares `dcb` with the upstream CLI; see the
[parity harness](test/parity/README.md). Report a parity change in the pull request.
`task docs:check` checks public Markdown and the documentation publishing manifest.
Task lists the individual checks, tool installation, and reference setup verbs.

CLI Docker tests run in name shards from `scripts/cli-integration-shards.tsv`.
Use `task integration:cli` for all shards or `task integration:cli -- users` for
one shard. The Docker, build, and host integration routes run every CLI shard.
CI uses the same Docker route. Each shard has the shared 20-minute Go test
budget. Add each new Docker test to exactly one shard; `task test` checks coverage.
The test bodies stay in `cmd/dcb` so that test additions need no package move.

Add a regression test for behavior changes. Keep reference submodules unchanged.
The project uses Intentional for release notes; retain one initial-release intent
until the first release. After that, include an intent for user-facing changes.

The owning organization's community health policies apply, including its code of
conduct, support guidance, and private security reporting policy.

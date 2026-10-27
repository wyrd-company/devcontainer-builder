---
relationships:
  references: [../../test/parity/README, ../../pkg/cli/inventory, ../../reference/cli/src/spec-node/devContainersSpecCLI]
---

# Upstream command option audit

This audit covers `reference/cli` at `131882da9e7faf4618fc37007a35bb8e1981cfdf`
(package version 0.86.0) and the `dcb` command surface.
Parity is the default. Missing commands, hidden flags and experimental flags remain
in scope. Only the approved native API/BuildKit requirement supports omissions.
No additional omission is proposed. A future omission that removes a supported
capability needs Bob's approval before implementation.

`implemented` means already-covered by a registered flag and handler.
`implement` means an implementation gap. `not applicable by design` means
omit-with-reason under the approved native API/BuildKit contract. `needs Bob's
decision` marks a proposed policy stricter than upstream that requires
approval. Counts are command-option pairs, not unique names. Known
value/behavior gaps stay `implement` even when a flag is registered.

`task parity -- --options-only` reads upstream's option tables and the real Cobra
command tree without Docker or Node. The report counts registered long flags,
including inherited help/version on each command. It records short aliases but
scores each canonical long option once. It never subtracts omissions. Registration
coverage is distinct from behavior parity and does not use these dispositions.
The pinned `up` table has **44 explicit options**, plus inherited help/version.
Registered option coverage is **154/253 (60.87%)**;
registration includes the three implemented session-folder pairs.

## Dispositions

- implemented: **154**.
- implement: **83**.
- not applicable by design: **15**.
- needs Bob's decision: **1**.

## Implementation units

Sizes are relative implementation/review scope: S is a narrow CLI/report change,
M spans a CLI handler and a library contract, L spans an integration or command
family and needs end-to-end validation. They are not elapsed-time commitments.
Each unit includes value validation, docs and relevant behavior evidence.
Cross-command options should share one implementation. Lockfile work precedes
upgrade reporting; feature packaging precedes publishing; registry information
supports outdated reporting and template metadata.

| Unit | Size | Option pairs | Scope and safety contract |
| --- | --- | --- | --- |
| sibling-unit | S | 17 | default-user-env-probe on up, set-up, run-user-commands and exec uses the shell probe with the approved 10-second timeout. The version flag on commands dcb does not have yet comes with those commands. |
| lifecycle | M | 7 | Lifecycle controls: skip-non-blocking-commands, prebuild, stop-for-personalization, expect-existing-container. |
| mounts | L | 14 | Workspace mounts and container selection: mount, workspace-mount-consistency, mount-workspace-git-root, mount-git-worktree-common-dir, id-label; mount-workspace-git-root defaults to true; mount-git-worktree-common-dir defaults to false; additional mounts are explicit. |
| environment | M | 5 | Remote environment and secrets: remote-env, secrets-file; lifecycle secret inputs are separate from configuration remoteEnv metadata. |
| data-folders | M | 17 | Registration: all folder options. Behavior: host staging, Compose object materialization/cleanup, and container markers are routed; see [state publication](state-publication.md). Session probe caches use the supplied folder with the upstream mode-specific JSON format. System patch markers are written by up and set-up. |
| terminal | S | 10 | Terminal dimensions: terminal-columns, terminal-rows. |
| lockfile | L | 4 | Feature lockfiles: experimental-lockfile, experimental-frozen-lockfile. |
| feature-controls | M | 9 | Feature mapping and metadata controls: skip-feature-auto-mapping, skip-persisting-customizations-from-features, omit-config-remote-env-from-metadata, omit-syntax-directive; persist remoteEnv in metadata by default; omit-config-remote-env-from-metadata defaults to false and omits it when enabled. |
| configuration | M | 5 | Configuration result controls: include-configuration, include-merged-configuration, include-features-configuration. |
| gpu | M | 1 | Implemented: all/detect/none with Engine runtime detection and device requests, including Compose GPU reservations; explicit GPU selection is preserved. |
| exporters | M | 1 | Build exporter specifications: output. |
| outdated | M | 9 | Outdated feature reporting. |
| upgrade | L | 7 | Feature upgrades and lockfile updates. |
| feature-tests | L | 14 | Feature test runner. |
| feature-package | M | 4 | Feature packaging; preserve existing output unless explicit cleanup is requested. |
| feature-publish | L | 4 | Feature publishing; publish only on explicit command invocation. |
| feature-info | M | 3 | Feature registry information. |
| feature-dependencies | M | 3 | Dependency graph reporting. |
| feature-docs | M | 7 | Feature documentation generation. |
| templates | S | 1 | Template command group. |
| template-apply | L | 8 | Template application; upstream overwrites colliding regular files. Collision policy needs Bob's decision before this future unit is implemented. |
| template-publish | L | 4 | Template packaging and publishing; publish only on explicit command invocation. |
| template-metadata | M | 2 | Template metadata. |
| template-docs | M | 5 | Template documentation generation. |

Upstream defaults govern implementation unless an approved requirement states
otherwise. Git-root mounting defaults to true. Configuration remoteEnv persists
in image metadata unless omit-config-remote-env-from-metadata is enabled.
A stricter policy requires Bob's decision; an additive opt-in does not itself
authorize changing the default. The approved process-credential requirement
governs native Git discovery: it reads with the credentials of its own process,
not those of a started `git`; see
[Process credentials](../overview.md#process-credentials). It removes no option.
Native Git discovery follows the latest stable Git release, which Bob
approved in
[Follow latest stable Git behavior](../decisions/follow-latest-stable-git-behavior.yml).
It applies the repository format rules of Git 2.55 built without Rust, which
Bob approved, whatever Git is installed; it does not check the host Git's
version or start `git`. Other Git versions and builds read some configuration
paths differently and accept other repository extensions, so the upstream CLI
can mount a different folder; see
[Workspace mounts](../usage.md#workspace-mounts-and-container-selection).
The full config read rejects invalid `core.ignorecase`, `core.filemode`,
`core.symlinks`, `core.trustctime`, `core.quotepath`, `core.ignorestat`,
`core.sparseCheckout`, `core.sparseCheckoutCone`, `core.protectHFS`,
`core.protectNTFS`, `core.precomposeUnicode`, `core.fsyncObjectFiles`,
`core.safecrlf`, `core.autocrlf` and `core.lockfilePid` boolean values after
reading all file, include and command-line inputs. `core.safecrlf` also accepts
`warn`, without regard to case. `core.autocrlf` also accepts `input`, without regard to case.
Native discovery follows current Git for `core.lockfilePid`; Git before 2.54
ignores that key.
Bob also approved one account-lookup difference: Linux release builds look up
accounts in `/etc/passwd` only and skip an `include.path` of `~name/` whose
account is not there; see
[Relation to the Dev Container CLI](../overview.md#relation-to-the-dev-container-cli).
`dcb` adds `--git-prefix` to up, exec, read-configuration and
run-user-commands, the commands with mount-workspace-git-root, and
`Options.GitPrefix` to `pkg/workspace`; upstream has neither. It names the
prefix of the host Git's build, where Git reads an `include.path` of
`%(prefix)/` followed by a relative path. Without it, `dcb` does not read that
file. Bob approved the extension and this default; see
[Explicit Git prefix setting](../decisions/explicit-git-prefix-setting.yml).
The extension removes no option and is not counted in the dispositions above.
Another deliberate difference from the latest stable Git needs Bob's approval.
The specification recommends mounting the repository root but does not define
how it is found or what happens when Git rejects the repository. Whether to keep
upstream's Git-dependent default, mount the requested folder by default, or
make the choice explicit is open in
[issue 44](https://github.com/wyrd-company/devcontainer-builder/issues/44); no
default change is approved.

Template commands are outside the current implementation scope. Template
application extracts non-omitted files into the destination and overwrites
colliding regular files without a collision prompt or content merge. It excludes
README.md, NOTES.md and devcontainer-template.json as well as omit-paths.
The workspace-folder row records the collision-policy decision for the future
template unit. Source: containerTemplatesOCI.ts:50 and
containerCollectionsOCI.ts:562.

## Commands and positional arguments

All 18 upstream leaf commands are included below, with the root and two command
groups. `dcb features` resolves/stages a configuration; it does not cover upstream's
feature authoring commands. The `templates` group, all its children, `outdated`,
`upgrade`, and all upstream `features` children are absent.

Positional arguments are part of each command unit but do not add flag score units:
`build [path]` maps to `build --workspace-folder`; `exec <cmd> [args..]` maps to
`exec <cmd> [args..]` (an explicit `--` also works). Missing units must also
implement `features test [target]`
(deprecated target; project-folder preferred), `features package <target>`,
`features publish <target>`, `features info <mode> <feature>`,
`templates publish <target>`, and `templates metadata <templateId>`.
Mode choices for feature info are manifest, tags, dependencies and verbose.
Command-shape differences alone are not an omission under the epic contract.

## Option inventory

The source column names the declaring table and line; help is a yargs built-in.
Aliases are recorded with their canonical option. `hidden` is part of the audit,
not a reason to exclude the option. The equivalent column states existing `dcb`
support, not a promised future flag. The sibling-unit label groups additional
features, dotfiles, environment probing and version support.

### (root)

| Upstream option | dcb equivalent | Disposition | Unit | Reason / source |
| --- | --- | --- | --- | --- |
| --help | --help | implemented | covered | Registered flag and handler exist; fixture parity remains a separate behavior measure. Source: yargs built-in help. |
| --version | --version | implemented | sibling-unit | Registered on every command; prints the version result in place of the command, as upstream. Source: devContainersSpecCLI.ts (.version). |

### build

| Upstream option | dcb equivalent | Disposition | Unit | Reason / source |
| --- | --- | --- | --- | --- |
| --user-data-folder | --user-data-folder | implemented | data-folders | Private host feature/build staging and Compose object materialization honor this folder; existing contents are preserved. Source: devContainersSpecCLI.ts:509. |
| --docker-path | none | not applicable by design | native-api | Approved native API requirement: runtime integrations use Engine/Compose APIs; executable path selection does not apply. Source: devContainersSpecCLI.ts:510. |
| --docker-compose-path | none | not applicable by design | native-api | Approved native API requirement: runtime integrations use Engine/Compose APIs; executable path selection does not apply. Source: devContainersSpecCLI.ts:511. |
| --workspace-folder | --workspace-folder | implemented | covered | Registered flag and handler exist; fixture parity remains a separate behavior measure. Source: devContainersSpecCLI.ts:512. |
| --config | --config | implemented | covered | Registered flag and handler exist; fixture parity remains a separate behavior measure. Source: devContainersSpecCLI.ts:513. |
| --log-level | --log-level | implemented | covered | Registered flag and handler exist; fixture parity remains a separate behavior measure. Source: devContainersSpecCLI.ts:514. |
| --log-format | --log-format | implemented | covered | Registered flag and handler exist; fixture parity remains a separate behavior measure. Source: devContainersSpecCLI.ts:515. |
| --no-cache | --no-cache | implemented | covered | Registered flag and handler exist; fixture parity remains a separate behavior measure. Source: devContainersSpecCLI.ts:516. |
| --image-name | --image-name | implemented | covered | Registered flag and handler exist; fixture parity remains a separate behavior measure. Source: devContainersSpecCLI.ts:517. |
| --cache-from | --cache-from | implemented | covered | Registered flag and handler exist; fixture parity remains a separate behavior measure. Source: devContainersSpecCLI.ts:518. |
| --cache-to | --cache-to | implemented | covered | Registered flag and handler exist; fixture parity remains a separate behavior measure. Source: devContainersSpecCLI.ts:519. |
| --buildkit | BuildKit through Engine API | not applicable by design | native-api | Approved native BuildKit requirement: the build contract requires native BuildKit; switching to the legacy Docker CLI builder is outside that contract. Source: devContainersSpecCLI.ts:520. |
| --platform | --platform | implemented | covered | Registered flag and handler exist; fixture parity remains a separate behavior measure. Source: devContainersSpecCLI.ts:521. |
| --push | --push | implemented | covered | Registered flag and handler exist; fixture parity remains a separate behavior measure. Source: devContainersSpecCLI.ts:522. |
| --label | --label | implemented | covered | Registered flag and handler exist; fixture parity remains a separate behavior measure. Source: devContainersSpecCLI.ts:523. |
| --output | --output | implemented | exporters | Buildx exporter specifications and short destinations through native BuildKit and the Engine API. Source: devContainersSpecCLI.ts:524. |
| --additional-features | --additional-features | implemented | sibling-unit | Registered; configuration features come first and win for an equal key, as upstream. Source: devContainersSpecCLI.ts:525. |
| --skip-feature-auto-mapping [hidden] | --skip-feature-auto-mapping | implemented | feature-controls | Registered hidden boolean, default false; resolver maps upstream legacy IDs unless enabled. Source: devContainersSpecCLI.ts:526. |
| --skip-persisting-customizations-from-features [hidden] | --skip-persisting-customizations-from-features | implemented | feature-controls | Registered hidden boolean, default false; omits new feature customizations while preserving inherited and configuration metadata. Source: devContainersSpecCLI.ts:527. |
| --experimental-lockfile [hidden] | --experimental-lockfile | implemented | lockfile | Digest-pinned resolution, integrity checks, frozen drift validation, and atomic publication after success. Source: devContainersSpecCLI.ts:528. |
| --experimental-frozen-lockfile [hidden] | --experimental-frozen-lockfile | implemented | lockfile | Digest-pinned resolution, integrity checks, frozen drift validation, and atomic publication after success. Source: devContainersSpecCLI.ts:529. |
| --omit-syntax-directive [hidden] | --omit-syntax-directive | implemented | feature-controls | Registered hidden boolean, default false; omits generated directives and filters authored syntax directives from Dockerfile frontend input. Source: devContainersSpecCLI.ts:530. |
| --help | --help | implemented | covered | Registered flag and handler exist; fixture parity remains a separate behavior measure. Source: yargs built-in help. |
| --version | --version | implemented | sibling-unit | Registered on every command; prints the version result in place of the command, as upstream. Source: devContainersSpecCLI.ts (.version). |

### exec

| Upstream option | dcb equivalent | Disposition | Unit | Reason / source |
| --- | --- | --- | --- | --- |
| --user-data-folder | --user-data-folder | implemented | data-folders | Private host feature/build staging and Compose object materialization honor this folder; existing contents are preserved. Source: devContainersSpecCLI.ts:1217. |
| --docker-path | none | not applicable by design | native-api | Approved native API requirement: runtime integrations use Engine/Compose APIs; executable path selection does not apply. Source: devContainersSpecCLI.ts:1218. |
| --docker-compose-path | none | not applicable by design | native-api | Approved native API requirement: runtime integrations use Engine/Compose APIs; executable path selection does not apply. Source: devContainersSpecCLI.ts:1219. |
| --container-data-folder | --container-data-folder | implemented | data-folders | Lifecycle and dotfiles markers honor this folder; exec writes no markers. Source: devContainersSpecCLI.ts:1220. |
| --container-system-data-folder | --container-system-data-folder | implemented | data-folders | Accepted with upstream default /var/devcontainer; up and set-up write the upstream environment and profile patch markers here. Source: devContainersSpecCLI.ts:1221. |
| --workspace-folder | --workspace-folder | implemented | covered | Registered flag and handler exist; fixture parity remains a separate behavior measure. Source: devContainersSpecCLI.ts:1222. |
| --mount-workspace-git-root | --mount-workspace-git-root | implemented | mounts | Default true, matching upstream; mounts the Git root found from the file system (no git subprocess); =false mounts the workspace folder alone. Source: devContainersSpecCLI.ts:1223. |
| --mount-git-worktree-common-dir | --mount-git-worktree-common-dir | implemented | mounts | Default false, matching upstream; mounts a relative-path worktree's common Git folder only when enabled. Source: devContainersSpecCLI.ts:1224. |
| --container-id | --container-id | implemented | covered | Registered flag and handler exist; fixture parity remains a separate behavior measure. Source: devContainersSpecCLI.ts:1225. |
| --id-label | --id-label | implemented | mounts | Selects by the full label set; single-container up sets only these labels; Compose keeps ownership labels beside them so the project-conflict check and workspace-scoped removal apply. Source: devContainersSpecCLI.ts:1226. |
| --config | --config | implemented | covered | Registered flag and handler exist; fixture parity remains a separate behavior measure. Source: devContainersSpecCLI.ts:1227. |
| --override-config | --override-config | implemented | covered | Registered flag and handler exist; fixture parity remains a separate behavior measure. Source: devContainersSpecCLI.ts:1228. |
| --log-level | --log-level | implemented | covered | Registered flag and handler exist; fixture parity remains a separate behavior measure. Source: devContainersSpecCLI.ts:1229. |
| --log-format | --log-format | implemented | covered | Registered flag and handler exist; fixture parity remains a separate behavior measure. Source: devContainersSpecCLI.ts:1230. |
| --terminal-columns | same name | implemented | terminal | Paired numeric dimensions size lifecycle and exec PTYs; zero leaves the size unset. Source: devContainersSpecCLI.ts:1231. |
| --terminal-rows | same name | implemented | terminal | Paired numeric dimensions size lifecycle and exec PTYs; zero leaves the size unset. Source: devContainersSpecCLI.ts:1232. |
| --default-user-env-probe | --default-user-env-probe | implemented | sibling-unit | Defaults to loginInteractiveShell; configuration userEnvProbe wins. Shell environment precedes remoteEnv; after 10 seconds warns and continues with an empty probe result. Source: devContainersSpecCLI.ts:1233. |
| --remote-env | --remote-env | implemented | covered | Adds variables to the exec environment, below configuration remoteEnv, as upstream. Source: devContainersSpecCLI.ts:1234; injectHeadless.ts:355. |
| --skip-feature-auto-mapping [hidden] | --skip-feature-auto-mapping | implemented | feature-controls | Registered hidden boolean, default false; resolver maps upstream legacy IDs unless enabled. Source: devContainersSpecCLI.ts:1235. |
| --help | --help | implemented | covered | Registered flag and handler exist; fixture parity remains a separate behavior measure. Source: yargs built-in help. |
| --version | --version | implemented | sibling-unit | Registered on every command; prints the version result in place of the command, as upstream. Source: devContainersSpecCLI.ts (.version). |

### features

| Upstream option | dcb equivalent | Disposition | Unit | Reason / source |
| --- | --- | --- | --- | --- |
| --help | --help | implemented | covered | Registered flag and handler exist; fixture parity remains a separate behavior measure. Source: yargs built-in help. |
| --version | --version | implemented | sibling-unit | Registered on every command; prints the version result in place of the command, as upstream. Source: devContainersSpecCLI.ts (.version). |

### features generate-docs

| Upstream option | dcb equivalent | Disposition | Unit | Reason / source |
| --- | --- | --- | --- | --- |
| --project-folder (-p) | none | implement | feature-docs | Command is absent; add the command and this option. Existing dcb features stages features and does not implement this subcommand. Source: featuresCLI/generateDocs.ts:13. |
| --registry (-r) | none | implement | feature-docs | Command is absent; add the command and this option. Existing dcb features stages features and does not implement this subcommand. Source: featuresCLI/generateDocs.ts:14. |
| --namespace (-n) | none | implement | feature-docs | Command is absent; add the command and this option. Existing dcb features stages features and does not implement this subcommand. Source: featuresCLI/generateDocs.ts:15. |
| --github-owner | none | implement | feature-docs | Command is absent; add the command and this option. Existing dcb features stages features and does not implement this subcommand. Source: featuresCLI/generateDocs.ts:16. |
| --github-repo | none | implement | feature-docs | Command is absent; add the command and this option. Existing dcb features stages features and does not implement this subcommand. Source: featuresCLI/generateDocs.ts:17. |
| --log-level | none | implement | feature-docs | Command is absent; add the command and this option. Existing dcb features stages features and does not implement this subcommand. Source: featuresCLI/generateDocs.ts:18. |
| --help | none | implement | feature-docs | Command is absent; add the command and this option. Existing dcb features stages features and does not implement this subcommand. Source: yargs built-in help. |
| --version | version command | implement | sibling-unit | dcb registers --version on every command; this command is not implemented yet. Source: devContainersSpecCLI.ts (.version). |

### features info

| Upstream option | dcb equivalent | Disposition | Unit | Reason / source |
| --- | --- | --- | --- | --- |
| --log-level | none | implement | feature-info | Command is absent; add the command and this option. Existing dcb features stages features and does not implement this subcommand. Source: featuresCLI/info.ts:15. |
| --output-format | none | implement | feature-info | Command is absent; add the command and this option. Existing dcb features stages features and does not implement this subcommand. Source: featuresCLI/info.ts:16. |
| --help | none | implement | feature-info | Command is absent; add the command and this option. Existing dcb features stages features and does not implement this subcommand. Source: yargs built-in help. |
| --version | version command | implement | sibling-unit | dcb registers --version on every command; this command is not implemented yet. Source: devContainersSpecCLI.ts (.version). |

### features package

| Upstream option | dcb equivalent | Disposition | Unit | Reason / source |
| --- | --- | --- | --- | --- |
| --output-folder (-o) | none | implement | feature-package | Command is absent; add the command and this option. Existing dcb features stages features and does not implement this subcommand. Source: collectionCommonUtils/package.ts:16. |
| --force-clean-output-folder (-f) | none | implement | feature-package | Command is absent; add the command and this option. Existing dcb features stages features and does not implement this subcommand. Source: collectionCommonUtils/package.ts:17. |
| --log-level | none | implement | feature-package | Command is absent; add the command and this option. Existing dcb features stages features and does not implement this subcommand. Source: collectionCommonUtils/package.ts:18. |
| --help | none | implement | feature-package | Command is absent; add the command and this option. Existing dcb features stages features and does not implement this subcommand. Source: yargs built-in help. |
| --version | version command | implement | sibling-unit | dcb registers --version on every command; this command is not implemented yet. Source: devContainersSpecCLI.ts (.version). |

### features publish

| Upstream option | dcb equivalent | Disposition | Unit | Reason / source |
| --- | --- | --- | --- | --- |
| --registry (-r) | none | implement | feature-publish | Command is absent; add the command and this option. Existing dcb features stages features and does not implement this subcommand. Source: collectionCommonUtils/publish.ts:12. |
| --namespace (-n) | none | implement | feature-publish | Command is absent; add the command and this option. Existing dcb features stages features and does not implement this subcommand. Source: collectionCommonUtils/publish.ts:13. |
| --log-level | none | implement | feature-publish | Command is absent; add the command and this option. Existing dcb features stages features and does not implement this subcommand. Source: collectionCommonUtils/publish.ts:14. |
| --help | none | implement | feature-publish | Command is absent; add the command and this option. Existing dcb features stages features and does not implement this subcommand. Source: yargs built-in help. |
| --version | version command | implement | sibling-unit | dcb registers --version on every command; this command is not implemented yet. Source: devContainersSpecCLI.ts (.version). |

### features resolve-dependencies

| Upstream option | dcb equivalent | Disposition | Unit | Reason / source |
| --- | --- | --- | --- | --- |
| --log-level | none | implement | feature-dependencies | Command is absent; add the command and this option. Existing dcb features stages features and does not implement this subcommand. Source: featuresCLI/resolveDependencies.ts:32. |
| --workspace-folder | none | implement | feature-dependencies | Command is absent; add the command and this option. Existing dcb features stages features and does not implement this subcommand. Source: featuresCLI/resolveDependencies.ts:33. |
| --help | none | implement | feature-dependencies | Command is absent; add the command and this option. Existing dcb features stages features and does not implement this subcommand. Source: yargs built-in help. |
| --version | version command | implement | sibling-unit | dcb registers --version on every command; this command is not implemented yet. Source: devContainersSpecCLI.ts (.version). |

### features test

| Upstream option | dcb equivalent | Disposition | Unit | Reason / source |
| --- | --- | --- | --- | --- |
| --project-folder (-p) | none | implement | feature-tests | Command is absent; add the command and this option. Existing dcb features stages features and does not implement this subcommand. Source: featuresCLI/test.ts:14. |
| --features (-f) | none | implement | feature-tests | Command is absent; add the command and this option. Existing dcb features stages features and does not implement this subcommand. Source: featuresCLI/test.ts:15. |
| --filter | none | implement | feature-tests | Command is absent; add the command and this option. Existing dcb features stages features and does not implement this subcommand. Source: featuresCLI/test.ts:16. |
| --global-scenarios-only | none | implement | feature-tests | Command is absent; add the command and this option. Existing dcb features stages features and does not implement this subcommand. Source: featuresCLI/test.ts:17. |
| --skip-scenarios | none | implement | feature-tests | Command is absent; add the command and this option. Existing dcb features stages features and does not implement this subcommand. Source: featuresCLI/test.ts:18. |
| --skip-autogenerated | none | implement | feature-tests | Command is absent; add the command and this option. Existing dcb features stages features and does not implement this subcommand. Source: featuresCLI/test.ts:19. |
| --skip-duplicated | none | implement | feature-tests | Command is absent; add the command and this option. Existing dcb features stages features and does not implement this subcommand. Source: featuresCLI/test.ts:20. |
| --permit-randomization | none | implement | feature-tests | Command is absent; add the command and this option. Existing dcb features stages features and does not implement this subcommand. Source: featuresCLI/test.ts:21. |
| --base-image (-i) | none | implement | feature-tests | Command is absent; add the command and this option. Existing dcb features stages features and does not implement this subcommand. Source: featuresCLI/test.ts:22. |
| --remote-user (-u) | none | implement | feature-tests | Command is absent; add the command and this option. Existing dcb features stages features and does not implement this subcommand. Source: featuresCLI/test.ts:23. |
| --log-level | none | implement | feature-tests | Command is absent; add the command and this option. Existing dcb features stages features and does not implement this subcommand. Source: featuresCLI/test.ts:24. |
| --preserve-test-containers | none | implement | feature-tests | Command is absent; add the command and this option. Existing dcb features stages features and does not implement this subcommand. Source: featuresCLI/test.ts:25. |
| --quiet (-q) | none | implement | feature-tests | Command is absent; add the command and this option. Existing dcb features stages features and does not implement this subcommand. Source: featuresCLI/test.ts:26. |
| --help | none | implement | feature-tests | Command is absent; add the command and this option. Existing dcb features stages features and does not implement this subcommand. Source: yargs built-in help. |
| --version | version command | implement | sibling-unit | dcb registers --version on every command; this command is not implemented yet. Source: devContainersSpecCLI.ts (.version). |

### outdated

| Upstream option | dcb equivalent | Disposition | Unit | Reason / source |
| --- | --- | --- | --- | --- |
| --user-data-folder | none | implement | outdated | Command is absent; add the command and this option. Existing dcb features stages features and does not implement this subcommand. Source: devContainersSpecCLI.ts:1119. |
| --workspace-folder | none | implement | outdated | Command is absent; add the command and this option. Existing dcb features stages features and does not implement this subcommand. Source: devContainersSpecCLI.ts:1120. |
| --config | none | implement | outdated | Command is absent; add the command and this option. Existing dcb features stages features and does not implement this subcommand. Source: devContainersSpecCLI.ts:1121. |
| --output-format | none | implement | outdated | Command is absent; add the command and this option. Existing dcb features stages features and does not implement this subcommand. Source: devContainersSpecCLI.ts:1122. |
| --log-level | none | implement | outdated | Command is absent; add the command and this option. Existing dcb features stages features and does not implement this subcommand. Source: devContainersSpecCLI.ts:1123. |
| --log-format | none | implement | outdated | Command is absent; add the command and this option. Existing dcb features stages features and does not implement this subcommand. Source: devContainersSpecCLI.ts:1124. |
| --terminal-columns | none | implement | outdated | Command is absent; add the command and this option. Existing dcb features stages features and does not implement this subcommand. Source: devContainersSpecCLI.ts:1125. |
| --terminal-rows | none | implement | outdated | Command is absent; add the command and this option. Existing dcb features stages features and does not implement this subcommand. Source: devContainersSpecCLI.ts:1126. |
| --help | none | implement | outdated | Command is absent; add the command and this option. Existing dcb features stages features and does not implement this subcommand. Source: yargs built-in help. |
| --version | version command | implement | sibling-unit | dcb registers --version on every command; this command is not implemented yet. Source: devContainersSpecCLI.ts (.version). |

### read-configuration

| Upstream option | dcb equivalent | Disposition | Unit | Reason / source |
| --- | --- | --- | --- | --- |
| --user-data-folder | --user-data-folder | implemented | data-folders | Private host feature/build staging and Compose object materialization honor this folder; existing contents are preserved. Source: devContainersSpecCLI.ts:960. |
| --docker-path | none | not applicable by design | native-api | Approved native API requirement: runtime integrations use Engine/Compose APIs; executable path selection does not apply. Source: devContainersSpecCLI.ts:961. |
| --docker-compose-path | none | not applicable by design | native-api | Approved native API requirement: runtime integrations use Engine/Compose APIs; executable path selection does not apply. Source: devContainersSpecCLI.ts:962. |
| --workspace-folder | --workspace-folder | implemented | covered | Registered flag and handler exist; fixture parity remains a separate behavior measure. Source: devContainersSpecCLI.ts:963. |
| --mount-workspace-git-root | --mount-workspace-git-root | implemented | mounts | Default true, matching upstream; mounts the Git root found from the file system (no git subprocess); =false mounts the workspace folder alone. Source: devContainersSpecCLI.ts:964. |
| --mount-git-worktree-common-dir | --mount-git-worktree-common-dir | implemented | mounts | Default false, matching upstream; mounts a relative-path worktree's common Git folder only when enabled. Source: devContainersSpecCLI.ts:965. |
| --container-id | --container-id | implemented | covered | Registered flag and handler exist; fixture parity remains a separate behavior measure. Source: devContainersSpecCLI.ts:966. |
| --id-label | --id-label | implemented | mounts | Selects by the full label set; single-container up sets only these labels; Compose keeps ownership labels beside them so the project-conflict check and workspace-scoped removal apply. Source: devContainersSpecCLI.ts:967. |
| --config | --config | implemented | covered | Registered flag and handler exist; fixture parity remains a separate behavior measure. Source: devContainersSpecCLI.ts:968. |
| --override-config | --override-config | implemented | covered | Registered flag and handler exist; fixture parity remains a separate behavior measure. Source: devContainersSpecCLI.ts:969. |
| --log-level | --log-level | implemented | covered | Registered flag and handler exist; fixture parity remains a separate behavior measure. Source: devContainersSpecCLI.ts:970. |
| --log-format | --log-format | implemented | covered | Registered flag and handler exist; fixture parity remains a separate behavior measure. Source: devContainersSpecCLI.ts:971. |
| --terminal-columns | same name | implemented | terminal | Paired numeric dimensions size lifecycle and exec PTYs; zero leaves the size unset. Source: devContainersSpecCLI.ts:972. |
| --terminal-rows | same name | implemented | terminal | Paired numeric dimensions size lifecycle and exec PTYs; zero leaves the size unset. Source: devContainersSpecCLI.ts:973. |
| --include-features-configuration | --include-features-configuration | implemented | covered | Resolved feature sets, values and sources are included on request. Source: devContainersSpecCLI.ts:974. |
| --include-merged-configuration | --include-merged-configuration | implemented | covered | Registered flag and handler exist; fixture parity remains a separate behavior measure. Source: devContainersSpecCLI.ts:975. |
| --additional-features | --additional-features | implemented | sibling-unit | Registered; contributes to the merged configuration only when no container exists, as upstream. Source: devContainersSpecCLI.ts:976. |
| --skip-feature-auto-mapping [hidden] | --skip-feature-auto-mapping | implemented | feature-controls | Registered hidden boolean, default false; resolver maps upstream legacy IDs unless enabled. Source: devContainersSpecCLI.ts:977. |
| --help | --help | implemented | covered | Registered flag and handler exist; fixture parity remains a separate behavior measure. Source: yargs built-in help. |
| --version | --version | implemented | sibling-unit | Registered on every command; prints the version result in place of the command, as upstream. Source: devContainersSpecCLI.ts (.version). |

### run-user-commands

Successful responses include the lifecycle `result`: `skipNonBlocking`, `prebuild`,
`stopForPersonalization`, or `done`. Boundaries apply even without a command or
when markers skip commands. Source: `injectHeadless.ts:364-410` and
`devContainersSpecCLI.ts:933-937`.

| Upstream option | dcb equivalent | Disposition | Unit | Reason / source |
| --- | --- | --- | --- | --- |
| --user-data-folder | --user-data-folder | implemented | data-folders | Private host feature/build staging and Compose object materialization honor this folder; existing contents are preserved. Source: devContainersSpecCLI.ts:753. |
| --docker-path | none | not applicable by design | native-api | Approved native API requirement: runtime integrations use Engine/Compose APIs; executable path selection does not apply. Source: devContainersSpecCLI.ts:754. |
| --docker-compose-path | none | not applicable by design | native-api | Approved native API requirement: runtime integrations use Engine/Compose APIs; executable path selection does not apply. Source: devContainersSpecCLI.ts:755. |
| --container-data-folder | --container-data-folder | implemented | data-folders | Lifecycle and dotfiles markers honor this folder; exec writes no markers. Source: devContainersSpecCLI.ts:756. |
| --container-system-data-folder | --container-system-data-folder | implemented | data-folders | Accepted with upstream default /var/devcontainer; up and set-up write the upstream environment and profile patch markers here. Source: devContainersSpecCLI.ts:757. |
| --workspace-folder | --workspace-folder | implemented | covered | Registered flag and handler exist; fixture parity remains a separate behavior measure. Source: devContainersSpecCLI.ts:758. |
| --mount-workspace-git-root | --mount-workspace-git-root | implemented | mounts | Default true, matching upstream; mounts the Git root found from the file system (no git subprocess); =false mounts the workspace folder alone. Source: devContainersSpecCLI.ts:759. |
| --mount-git-worktree-common-dir | --mount-git-worktree-common-dir | implemented | mounts | Default false, matching upstream; mounts a relative-path worktree's common Git folder only when enabled. Source: devContainersSpecCLI.ts:760. |
| --container-id | --container-id | implemented | covered | Registered flag and handler exist; fixture parity remains a separate behavior measure. Source: devContainersSpecCLI.ts:761. |
| --id-label | --id-label | implemented | mounts | Selects by the full label set; single-container up sets only these labels; Compose keeps ownership labels beside them so the project-conflict check and workspace-scoped removal apply. Source: devContainersSpecCLI.ts:762. |
| --config | --config | implemented | covered | Registered flag and handler exist; fixture parity remains a separate behavior measure. Source: devContainersSpecCLI.ts:763. |
| --override-config | --override-config | implemented | covered | Registered flag and handler exist; fixture parity remains a separate behavior measure. Source: devContainersSpecCLI.ts:764. |
| --log-level | --log-level | implemented | covered | Registered flag and handler exist; fixture parity remains a separate behavior measure. Source: devContainersSpecCLI.ts:765. |
| --log-format | --log-format | implemented | covered | Registered flag and handler exist; fixture parity remains a separate behavior measure. Source: devContainersSpecCLI.ts:766. |
| --terminal-columns | same name | implemented | terminal | Paired numeric dimensions size lifecycle and exec PTYs; zero leaves the size unset. Source: devContainersSpecCLI.ts:767. |
| --terminal-rows | same name | implemented | terminal | Paired numeric dimensions size lifecycle and exec PTYs; zero leaves the size unset. Source: devContainersSpecCLI.ts:768. |
| --default-user-env-probe | --default-user-env-probe | implemented | sibling-unit | Defaults to loginInteractiveShell; configuration userEnvProbe wins. Shell environment precedes remoteEnv; after 10 seconds warns and continues with an empty probe result. Source: devContainersSpecCLI.ts:769. |
| --skip-non-blocking-commands | --skip-non-blocking-commands | implemented | covered | Stops after merged waitFor (default updateContentCommand), without background continuation. Docker and Compose behavior tests. Source: devContainersSpecCLI.ts:770. |
| --prebuild | --prebuild | implemented | covered | Stops after updateContentCommand and reruns that hook with a current marker. Docker and Compose behavior tests. Source: devContainersSpecCLI.ts:771. |
| --stop-for-personalization | --stop-for-personalization | implemented | covered | Stops after dotfiles and before postStartCommand. Docker and Compose behavior tests. Source: devContainersSpecCLI.ts:772. |
| --remote-env | --remote-env | implemented | environment | Registered; adds variables to lifecycle commands and the dotfiles install, below configuration remoteEnv, as upstream. Not stored. Source: devContainersSpecCLI.ts:773. |
| --skip-feature-auto-mapping [hidden] | --skip-feature-auto-mapping | implemented | feature-controls | Registered hidden boolean, default false; resolver maps upstream legacy IDs unless enabled. Source: devContainersSpecCLI.ts:774. |
| --skip-post-attach | --skip-post-attach | implemented | covered | Registered flag and handler exist; fixture parity remains a separate behavior measure. Source: devContainersSpecCLI.ts:775. |
| --dotfiles-repository | --dotfiles-repository | implemented | sibling-unit | Registered; installs after postCreateCommand once per container. A failed install warns and the next run retries. Source: devContainersSpecCLI.ts:776. |
| --dotfiles-install-command | --dotfiles-install-command | implemented | sibling-unit | Registered; runs in the clone in place of the default install-script search. Source: devContainersSpecCLI.ts:777. |
| --dotfiles-target-path | --dotfiles-target-path | implemented | sibling-unit | Registered; default ~/dotfiles. Source: devContainersSpecCLI.ts:778. |
| --container-session-data-folder | --container-session-data-folder | implemented | data-folders | Mode-specific user environment probe caches are read and written in this folder; failures are non-fatal. Source: devContainersSpecCLI.ts:779. |
| --secrets-file | --secrets-file | implemented | environment | Registered; secret variables for lifecycle commands and the dotfiles install, above remoteEnv. Not stored; masked in stderr and the result. Source: devContainersSpecCLI.ts:780. |
| --help | --help | implemented | covered | Registered flag and handler exist; fixture parity remains a separate behavior measure. Source: yargs built-in help. |
| --version | --version | implemented | sibling-unit | Registered on every command; prints the version result in place of the command, as upstream. Source: devContainersSpecCLI.ts (.version). |

### set-up

| Upstream option | dcb equivalent | Disposition | Unit | Reason / source |
| --- | --- | --- | --- | --- |
| --docker-path | none | not applicable by design | native-api | Approved native API requirement: runtime integrations use Engine/Compose APIs; executable path selection does not apply. Source: devContainersSpecCLI.ts:340. |
| --container-data-folder | --container-data-folder | implemented | data-folders | Lifecycle and dotfiles markers honor this folder; exec writes no markers. Source: devContainersSpecCLI.ts:341. |
| --container-system-data-folder | --container-system-data-folder | implemented | data-folders | Accepted with upstream default /var/devcontainer; up and set-up write the upstream environment and profile patch markers here. Source: devContainersSpecCLI.ts:342. |
| --container-id | --container-id | implemented | covered | Registered flag and handler exist; fixture parity remains a separate behavior measure. Source: devContainersSpecCLI.ts:343. |
| --config | --config | implemented | covered | Registered flag and handler exist; fixture parity remains a separate behavior measure. Source: devContainersSpecCLI.ts:344. |
| --log-level | --log-level | implemented | covered | Registered flag and handler exist; fixture parity remains a separate behavior measure. Source: devContainersSpecCLI.ts:345. |
| --log-format | --log-format | implemented | covered | Registered flag and handler exist; fixture parity remains a separate behavior measure. Source: devContainersSpecCLI.ts:346. |
| --terminal-columns | same name | implemented | terminal | Paired numeric dimensions size lifecycle and exec PTYs; zero leaves the size unset. Source: devContainersSpecCLI.ts:347. |
| --terminal-rows | same name | implemented | terminal | Paired numeric dimensions size lifecycle and exec PTYs; zero leaves the size unset. Source: devContainersSpecCLI.ts:348. |
| --default-user-env-probe | --default-user-env-probe | implemented | sibling-unit | Defaults to loginInteractiveShell; configuration userEnvProbe wins. Shell environment precedes remoteEnv; after 10 seconds warns and continues with an empty probe result. Source: devContainersSpecCLI.ts:349. |
| --skip-post-create | --skip-post-create | implemented | covered | Registered flag and handler exist; fixture parity remains a separate behavior measure. Source: devContainersSpecCLI.ts:350. |
| --skip-non-blocking-commands | --skip-non-blocking-commands | implemented | covered | Stops after merged waitFor (default updateContentCommand), without background continuation. Docker and Compose behavior tests. Source: devContainersSpecCLI.ts:351. |
| --user-data-folder | --user-data-folder | implemented | data-folders | Private host feature/build staging and Compose object materialization honor this folder; existing contents are preserved. Source: devContainersSpecCLI.ts:352. |
| --remote-env | --remote-env | implemented | environment | Registered; adds variables to lifecycle commands and the dotfiles install, below configuration remoteEnv, as upstream. Not stored. Source: devContainersSpecCLI.ts:353. |
| --dotfiles-repository | --dotfiles-repository | implemented | sibling-unit | Registered; installs after postCreateCommand once per container. A failed install warns and the next run retries. Source: devContainersSpecCLI.ts:354. |
| --dotfiles-install-command | --dotfiles-install-command | implemented | sibling-unit | Registered; runs in the clone in place of the default install-script search. Source: devContainersSpecCLI.ts:355. |
| --dotfiles-target-path | --dotfiles-target-path | implemented | sibling-unit | Registered; default ~/dotfiles. Source: devContainersSpecCLI.ts:356. |
| --container-session-data-folder | --container-session-data-folder | implemented | data-folders | Mode-specific user environment probe caches are read and written in this folder; failures are non-fatal. Source: devContainersSpecCLI.ts:357. |
| --include-configuration | --include-configuration | implemented | covered | Configuration snapshot is included on request. Source: devContainersSpecCLI.ts:358. |
| --include-merged-configuration | --include-merged-configuration | implemented | covered | Merged configuration snapshot is included on request. Source: devContainersSpecCLI.ts:359. |
| --help | --help | implemented | covered | Registered flag and handler exist; fixture parity remains a separate behavior measure. Source: yargs built-in help. |
| --version | --version | implemented | sibling-unit | Registered on every command; prints the version result in place of the command, as upstream. Source: devContainersSpecCLI.ts (.version). |

### templates

| Upstream option | dcb equivalent | Disposition | Unit | Reason / source |
| --- | --- | --- | --- | --- |
| --help | none | implement | templates | Command is absent; add the command and this option. Existing dcb features stages features and does not implement this subcommand. Source: yargs built-in help. |
| --version | version command | implement | sibling-unit | dcb registers --version on every command; this command is not implemented yet. Source: devContainersSpecCLI.ts (.version). |

### templates apply

| Upstream option | dcb equivalent | Disposition | Unit | Reason / source |
| --- | --- | --- | --- | --- |
| --workspace-folder (-w) | none | needs Bob's decision | template-apply | Upstream overwrites colliding non-omitted regular files. A: preserve upstream overwrite default. B (recommended): refuse collisions by default with an additive overwrite flag. Decide the stricter policy in the future template unit; no policy is approved here. Sources: templatesCLI/apply.ts:14, containerTemplatesOCI.ts:50, containerCollectionsOCI.ts:562. |
| --template-id (-t) | none | implement | template-apply | Command is absent; add the command and this option. Existing dcb features stages features and does not implement this subcommand. Source: templatesCLI/apply.ts:15. |
| --template-args (-a) | none | implement | template-apply | Command is absent; add the command and this option. Existing dcb features stages features and does not implement this subcommand. Source: templatesCLI/apply.ts:16. |
| --features (-f) | none | implement | template-apply | Command is absent; add the command and this option. Existing dcb features stages features and does not implement this subcommand. Source: templatesCLI/apply.ts:17. |
| --log-level | none | implement | template-apply | Command is absent; add the command and this option. Existing dcb features stages features and does not implement this subcommand. Source: templatesCLI/apply.ts:18. |
| --tmp-dir | none | implement | template-apply | Command is absent; add the command and this option. Existing dcb features stages features and does not implement this subcommand. Source: templatesCLI/apply.ts:19. |
| --omit-paths | none | implement | template-apply | Command is absent; add the command and this option. Existing dcb features stages features and does not implement this subcommand. Source: templatesCLI/apply.ts:20. |
| --help | none | implement | template-apply | Command is absent; add the command and this option. Existing dcb features stages features and does not implement this subcommand. Source: yargs built-in help. |
| --version | version command | implement | sibling-unit | dcb registers --version on every command; this command is not implemented yet. Source: devContainersSpecCLI.ts (.version). |

### templates generate-docs

| Upstream option | dcb equivalent | Disposition | Unit | Reason / source |
| --- | --- | --- | --- | --- |
| --project-folder (-p) | none | implement | template-docs | Command is absent; add the command and this option. Existing dcb features stages features and does not implement this subcommand. Source: templatesCLI/generateDocs.ts:13. |
| --github-owner | none | implement | template-docs | Command is absent; add the command and this option. Existing dcb features stages features and does not implement this subcommand. Source: templatesCLI/generateDocs.ts:14. |
| --github-repo | none | implement | template-docs | Command is absent; add the command and this option. Existing dcb features stages features and does not implement this subcommand. Source: templatesCLI/generateDocs.ts:15. |
| --log-level | none | implement | template-docs | Command is absent; add the command and this option. Existing dcb features stages features and does not implement this subcommand. Source: templatesCLI/generateDocs.ts:16. |
| --help | none | implement | template-docs | Command is absent; add the command and this option. Existing dcb features stages features and does not implement this subcommand. Source: yargs built-in help. |
| --version | version command | implement | sibling-unit | dcb registers --version on every command; this command is not implemented yet. Source: devContainersSpecCLI.ts (.version). |

### templates metadata

| Upstream option | dcb equivalent | Disposition | Unit | Reason / source |
| --- | --- | --- | --- | --- |
| --log-level | none | implement | template-metadata | Command is absent; add the command and this option. Existing dcb features stages features and does not implement this subcommand. Source: templatesCLI/metadata.ts:13. |
| --help | none | implement | template-metadata | Command is absent; add the command and this option. Existing dcb features stages features and does not implement this subcommand. Source: yargs built-in help. |
| --version | version command | implement | sibling-unit | dcb registers --version on every command; this command is not implemented yet. Source: devContainersSpecCLI.ts (.version). |

### templates publish

| Upstream option | dcb equivalent | Disposition | Unit | Reason / source |
| --- | --- | --- | --- | --- |
| --registry (-r) | none | implement | template-publish | Command is absent; add the command and this option. Existing dcb features stages features and does not implement this subcommand. Source: collectionCommonUtils/publish.ts:12. |
| --namespace (-n) | none | implement | template-publish | Command is absent; add the command and this option. Existing dcb features stages features and does not implement this subcommand. Source: collectionCommonUtils/publish.ts:13. |
| --log-level | none | implement | template-publish | Command is absent; add the command and this option. Existing dcb features stages features and does not implement this subcommand. Source: collectionCommonUtils/publish.ts:14. |
| --help | none | implement | template-publish | Command is absent; add the command and this option. Existing dcb features stages features and does not implement this subcommand. Source: yargs built-in help. |
| --version | version command | implement | sibling-unit | dcb registers --version on every command; this command is not implemented yet. Source: devContainersSpecCLI.ts (.version). |

### up

| Upstream option | dcb equivalent | Disposition | Unit | Reason / source |
| --- | --- | --- | --- | --- |
| --docker-path | none | not applicable by design | native-api | Approved native API requirement: runtime integrations use Engine/Compose APIs; executable path selection does not apply. Source: devContainersSpecCLI.ts:102. |
| --docker-compose-path | none | not applicable by design | native-api | Approved native API requirement: runtime integrations use Engine/Compose APIs; executable path selection does not apply. Source: devContainersSpecCLI.ts:103. |
| --container-data-folder | --container-data-folder | implemented | data-folders | Lifecycle and dotfiles markers honor this folder; exec writes no markers. Source: devContainersSpecCLI.ts:104. |
| --container-system-data-folder | --container-system-data-folder | implemented | data-folders | Accepted with upstream default /var/devcontainer; up and set-up write the upstream environment and profile patch markers here. Source: devContainersSpecCLI.ts:105. |
| --workspace-folder | --workspace-folder | implemented | covered | Registered flag and handler exist; fixture parity remains a separate behavior measure. Source: devContainersSpecCLI.ts:106. |
| --workspace-mount-consistency | --workspace-mount-consistency | implemented | mounts | Default cached; applied to the workspace mount on hosts other than Linux, as upstream. Source: devContainersSpecCLI.ts:107. |
| --gpu-availability | --gpu-availability | implemented | gpu | Default detect, matching upstream; all/detect/none use Engine runtime detection and device requests, including Compose GPU reservations; explicit GPU selection is preserved. Source: devContainersSpecCLI.ts:108. |
| --mount-workspace-git-root | --mount-workspace-git-root | implemented | mounts | Default true, matching upstream; mounts the Git root found from the file system (no git subprocess); =false mounts the workspace folder alone. Source: devContainersSpecCLI.ts:109. |
| --mount-git-worktree-common-dir | --mount-git-worktree-common-dir | implemented | mounts | Default false, matching upstream; mounts a relative-path worktree's common Git folder only when enabled. Source: devContainersSpecCLI.ts:110. |
| --id-label | --id-label | implemented | mounts | Single-container: selects, reuses and removes only by the full label set and sets only these labels. Compose, as upstream: selects the primary by project and service, sets the id labels beside the ownership labels at creation, and removal stays workspace/project scoped, so the project-conflict check applies. Source: devContainersSpecCLI.ts:111. |
| --config | --config | implemented | covered | Registered flag and handler exist; fixture parity remains a separate behavior measure. Source: devContainersSpecCLI.ts:112. |
| --override-config | --override-config | implemented | covered | Registered flag and handler exist; fixture parity remains a separate behavior measure. Source: devContainersSpecCLI.ts:113. |
| --log-level | --log-level | implemented | covered | Registered flag and handler exist; fixture parity remains a separate behavior measure. Source: devContainersSpecCLI.ts:114. |
| --log-format | --log-format | implemented | covered | Registered flag and handler exist; fixture parity remains a separate behavior measure. Source: devContainersSpecCLI.ts:115. |
| --terminal-columns | same name | implemented | terminal | Paired numeric dimensions size lifecycle and exec PTYs; zero leaves the size unset. Source: devContainersSpecCLI.ts:116. |
| --terminal-rows | same name | implemented | terminal | Paired numeric dimensions size lifecycle and exec PTYs; zero leaves the size unset. Source: devContainersSpecCLI.ts:117. |
| --default-user-env-probe | --default-user-env-probe | implemented | sibling-unit | Defaults to loginInteractiveShell; configuration userEnvProbe wins. Shell environment precedes remoteEnv; after 10 seconds warns and continues with an empty probe result. Source: devContainersSpecCLI.ts:118. |
| --update-remote-user-uid-default | --update-remote-user-uid-default | implemented | covered | Registered flag and handler exist; fixture parity remains a separate behavior measure. Source: devContainersSpecCLI.ts:119. |
| --remove-existing-container | --remove-existing-container | implemented | covered | Registered flag and handler exist; fixture parity remains a separate behavior measure. Source: devContainersSpecCLI.ts:120. |
| --build-no-cache | --build-no-cache | implemented | covered | Registered flag and handler exist; fixture parity remains a separate behavior measure. Source: devContainersSpecCLI.ts:121. |
| --expect-existing-container | --expect-existing-container | implemented | covered | Checks existence before build or removal; accepts stopped containers and preserves Compose containers on drift. Docker and Compose behavior tests. Source: devContainersSpecCLI.ts:122. |
| --skip-post-create | --skip-post-create | implemented | covered | Registered flag and handler exist; fixture parity remains a separate behavior measure. Source: devContainersSpecCLI.ts:123. |
| --skip-non-blocking-commands | --skip-non-blocking-commands | implemented | covered | Stops after merged waitFor (default updateContentCommand), without background continuation. Docker and Compose behavior tests. Source: devContainersSpecCLI.ts:124. |
| --prebuild | --prebuild | implemented | covered | Stops after updateContentCommand and reruns that hook with a current marker. Docker and Compose behavior tests. Source: devContainersSpecCLI.ts:125. |
| --user-data-folder | --user-data-folder | implemented | data-folders | Private host feature/build staging and Compose object materialization honor this folder; existing contents are preserved. Source: devContainersSpecCLI.ts:126. |
| --mount | --mount | implemented | mounts | Bind/volume mounts through the Engine API and Compose; no additional mounts unless given. Source: devContainersSpecCLI.ts:127. |
| --remote-env | --remote-env | implemented | environment | Registered; adds variables to lifecycle commands and the dotfiles install, below configuration remoteEnv, as upstream. Not stored. Source: devContainersSpecCLI.ts:128. |
| --cache-from | --cache-from | implemented | covered | Registered flag and handler exist; fixture parity remains a separate behavior measure. Source: devContainersSpecCLI.ts:129. |
| --cache-to | --cache-to | implemented | covered | Registered flag and handler exist; fixture parity remains a separate behavior measure. Source: devContainersSpecCLI.ts:130. |
| --buildkit | BuildKit through Engine API | not applicable by design | native-api | Approved native BuildKit requirement: the build contract requires native BuildKit; switching to the legacy Docker CLI builder is outside that contract. Source: devContainersSpecCLI.ts:131. |
| --additional-features | --additional-features | implemented | sibling-unit | Registered; configuration features come first and win for an equal key, as upstream. Source: devContainersSpecCLI.ts:132. |
| --skip-feature-auto-mapping [hidden] | --skip-feature-auto-mapping | implemented | feature-controls | Registered hidden boolean, default false; resolver maps upstream legacy IDs unless enabled. Source: devContainersSpecCLI.ts:133. |
| --skip-post-attach | --skip-post-attach | implemented | covered | Registered flag and handler exist; fixture parity remains a separate behavior measure. Source: devContainersSpecCLI.ts:134. |
| --dotfiles-repository | --dotfiles-repository | implemented | sibling-unit | Registered; installs after postCreateCommand once per container. A failed install warns and the next run retries. Source: devContainersSpecCLI.ts:135. |
| --dotfiles-install-command | --dotfiles-install-command | implemented | sibling-unit | Registered; runs in the clone in place of the default install-script search. Source: devContainersSpecCLI.ts:136. |
| --dotfiles-target-path | --dotfiles-target-path | implemented | sibling-unit | Registered; default ~/dotfiles. Source: devContainersSpecCLI.ts:137. |
| --container-session-data-folder | --container-session-data-folder | implemented | data-folders | Mode-specific user environment probe caches are read and written in this folder; failures are non-fatal. Source: devContainersSpecCLI.ts:138. |
| --omit-config-remote-env-from-metadata [hidden] | --omit-config-remote-env-from-metadata | implemented | feature-controls | Registered hidden boolean, default false; persists configuration remoteEnv by default and omits it when enabled; runtime remoteEnv stays available. Source: devContainersSpecCLI.ts:139. |
| --secrets-file | --secrets-file | implemented | environment | Registered; secret variables for lifecycle commands and the dotfiles install, above remoteEnv. Not stored; masked in stderr and the result. Source: devContainersSpecCLI.ts:140. |
| --experimental-lockfile [hidden] | --experimental-lockfile | implemented | lockfile | Digest-pinned resolution, integrity checks, frozen drift validation, and atomic publication after success. Source: devContainersSpecCLI.ts:141. |
| --experimental-frozen-lockfile [hidden] | --experimental-frozen-lockfile | implemented | lockfile | Digest-pinned resolution, integrity checks, frozen drift validation, and atomic publication after success. Source: devContainersSpecCLI.ts:142. |
| --omit-syntax-directive [hidden] | --omit-syntax-directive | implemented | feature-controls | Registered hidden boolean, default false; omits generated directives and filters authored syntax directives from Dockerfile frontend input. Source: devContainersSpecCLI.ts:143. |
| --include-configuration | --include-configuration | implemented | covered | Configuration snapshot is included on request. Source: devContainersSpecCLI.ts:144. |
| --include-merged-configuration | --include-merged-configuration | implemented | covered | Merged configuration snapshot is included on request. Source: devContainersSpecCLI.ts:145. |
| --help | --help | implemented | covered | Registered flag and handler exist; fixture parity remains a separate behavior measure. Source: yargs built-in help. |
| --version | --version | implemented | sibling-unit | Registered on every command; prints the version result in place of the command, as upstream. Source: devContainersSpecCLI.ts (.version). |

### upgrade

| Upstream option | dcb equivalent | Disposition | Unit | Reason / source |
| --- | --- | --- | --- | --- |
| --workspace-folder | none | implement | upgrade | Command is absent; add the command and this option. Existing dcb features stages features and does not implement this subcommand. Source: upgradeCommand.ts:26. |
| --docker-path | none | not applicable by design | native-api | Approved native API requirement: runtime integrations use Engine/Compose APIs; executable path selection does not apply. Source: upgradeCommand.ts:27. |
| --docker-compose-path | none | not applicable by design | native-api | Approved native API requirement: runtime integrations use Engine/Compose APIs; executable path selection does not apply. Source: upgradeCommand.ts:28. |
| --config | none | implement | upgrade | Command is absent; add the command and this option. Existing dcb features stages features and does not implement this subcommand. Source: upgradeCommand.ts:29. |
| --log-level | none | implement | upgrade | Command is absent; add the command and this option. Existing dcb features stages features and does not implement this subcommand. Source: upgradeCommand.ts:30. |
| --dry-run | none | implement | upgrade | Command is absent; add the command and this option. Existing dcb features stages features and does not implement this subcommand. Source: upgradeCommand.ts:31. |
| --feature (-f) [hidden] | none | implement | upgrade | Command is absent; add the command and this option. Existing dcb features stages features and does not implement this subcommand. Source: upgradeCommand.ts:33. |
| --target-version (-v) [hidden] | none | implement | upgrade | Command is absent; add the command and this option. Existing dcb features stages features and does not implement this subcommand. Source: upgradeCommand.ts:34. |
| --help | none | implement | upgrade | Command is absent; add the command and this option. Existing dcb features stages features and does not implement this subcommand. Source: yargs built-in help. |
| --version | version command | implement | sibling-unit | dcb registers --version on every command; this command is not implemented yet. Source: devContainersSpecCLI.ts (.version). |

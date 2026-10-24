---
relationships:
  references: [upstream-options, ../user/usage, ../../pkg/host/dockercompose/object_state, ../../pkg/host/dockercompose/objects]
---

# Host state publication

## Compose configs and secrets

`Host.Up` passes the selected `Options.UserDataFolder` to a private run-folder
allocator. CLI `up` forwards `--user-data-folder` to this host option. The default
parent is `$TMPDIR/dcb-compose`. The allocator normalizes the parent before
creating `objects-*` directories. Each run owns a separate directory; none is a
cache or a reusable completion marker.

File sources pass through without copying. Inline configs and environment-backed
configs/secrets write mode `0400` files below the private directory's `configs`
or `secrets` child. The materializer also handles in-memory secret content, but
the pinned Compose loader schema rejects authored secret `content:`.
The containing run directory is mode `0700`. External and driver sources retain
their existing unsupported-source errors. UID/GID/mode overrides retain the
standalone bind-mount behavior. Source files and unrelated parent contents are
outside cleanup ownership.

Before any payload is written, an unmounted Docker volume records the absolute
private root, workspace, and Compose project in labels. Labels contain no payload.
The returned volume must match the requested root, workspace and project; a
name collision preserves the existing record and fails before any payload write.
A cancelled allocation can leave an empty directory or an ownership record;
`Remove` can discover a record without containers or source configuration.
A process killed after payload creation leaves its record for workspace teardown.

Each file is written through a temporary sibling and renamed before the mount
consumer receives its path. A failed file publication removes its temporary
file. No consumer discovers another run's in-progress directory. Whole-directory
sibling publication is unnecessary: the root is private, and ownership records
are cleanup indexes, not readable completion markers.

`Host.Remove` snapshots workspace ownership records and validates each project's
container ownership before destructive operations. Successful container removal
precedes backing-file deletion. File cleanup precedes ownership-record deletion.
A failed cleanup returns an error and retains the record for retry. Project-selected
removal filters ownership to that project through the same selector as recreation. Other
workspaces' records and private directories remain outside that snapshot. CLI
`down` consults this runtime ownership without falling back to the default config,
then continues the selected configuration's removal path. Explicit ID-label
selection stays on its normal removal path.

Before `Up` changes containers, it snapshots this workspace/project's ownership
records that existing project containers mount. After successful bring-up and
lifecycle commands, it removes only snapshot roots that no container mounts.
The consumer check includes stopped containers and containers outside the project.
It resolves host-visible symlink aliases before comparing object and mount paths.
A reused service can keep an older shared root. Unmounted records are excluded
from this retirement snapshot so an in-flight producer's files are preserved.
File cleanup and record deletion use the same validation and retry path as
`Remove`; an error is returned. Failed or interrupted runs remain discoverable
for explicit workspace teardown.

Behavioral evidence is separate from option-registration coverage:

- `TestUserDataFolderReadConfigurationHandler` proves retained feature staging
  with in-memory empty Docker discovery.
- `TestDockerComposeObjectCleanupCLI` proves real process-boundary folder routing,
  container-visible values, down without the creation flag, malformed and absent
  configuration, cleanup failure/retry, idempotence, and parent sentinels.
- `TestDockerComposeObjectSelectedConfigCLI` proves default Compose config cannot
  swallow selected non-Compose or ID-label teardown, including after runtime cleanup.
- `TestComposeObjectRecreateCleanupIntegration` proves retired-root absence,
  shared and external consumer preservation, workspace/project isolation,
  in-flight producer preservation and cleanup/consumer lookup failures.
- `TestComposeObjectRemoveIntegration` proves host Remove for default and selected
  parents, lost containers, changed configuration, file-source preservation and
  independent workspace isolation.
  `TestComposeObjectWorkspaceOwnershipIntegration` preserves another workspace's
  ownership even when only records remain in a shared project.
- `TestComposeObjectPublicationIntegration` holds ownership publication while a
  second writer completes; the first then succeeds or fails. It proves private
  output, retained ownership on cleanup failure and a fresh-host retry.
- `TestComposeObjectCreationFailureIntegration` proves cleanup after real
  container creation failure; `TestComposeObjectInterruptedProcessIntegration`
  proves cleanup after killing the producer process.
- `TestComposeObjectLedgerRemovalRetryIntegration` proves retry when a real
  Docker container holds the ownership volume.
  `TestComposeObjectContainerRemovalFailureIntegration` forwards a real stale
  deletion to Docker and proves that its failure preserves backing objects.
- `TestObjectMaterializerErrorsDoNotDisclosePayload` proves local filesystem
  errors contain none of the individually injected payload spellings.
- `TestComposeObjectOwnershipCollisionIntegration` proves that native Docker
  creation cannot silently adopt another owner's existing record.
- `TestComposeObjectOwnershipValidationIntegration` proves malformed deletion
  records preserve unowned files.
- `TestObjectMaterializerPrivatePublication` proves bounded object filenames and
  temporary-file cleanup on successful and failed publication.

## Other host writers

Feature resolution and build contexts allocate unique private staging folders
under the selected host data folder. Consumers receive prepared paths; ordinary
metadata callers clean up their private staging. Read-configuration retains
published feature paths. Explicit build work directories remain caller-selected.
Feature alias metadata extraction uses a private child inside its staging
folder. Interrupted alias scratch in caller-owned feature output remains backlog
work; it is not covered by Compose teardown.

Container lifecycle and dotfiles markers are container writers. Lifecycle hook
markers retain upstream write-before-hook semantics. Dotfiles completion markers
retain after-success publication. On `up` and `set-up`, root system patch markers
are created before the `/etc/environment` and `/etc/profile` edits, including
under `--skip-post-create`. The selected container system data folder holds
these markers. Session probe-cache persistence is not implemented.
`Resolver.CacheDir` has no current cache reader/writer.

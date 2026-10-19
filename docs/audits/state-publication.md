---
relationships:
  references: [upstream-options, ../usage, ../../pkg/host/dockercompose/object_state, ../../pkg/host/dockercompose/objects]
---

# Host state publication

## Compose configs and secrets

`Host.Up` passes the selected `Options.UserDataFolder` to a private run-folder
allocator. CLI `up` forwards `--user-data-folder` to this host option. The default
parent is `$TMPDIR/dcb-compose`. The allocator normalizes the parent before
creating `objects-*` directories. Each run owns a separate directory; none is a
cache or a reusable completion marker.

File sources pass through without copying. Environment and inline sources write
mode `0400` files below the private directory's `configs` or `secrets` child.
The containing run directory is mode `0700`. External and driver sources retain
their existing unsupported-source errors. UID/GID/mode overrides retain the
standalone bind-mount behavior. Source files and unrelated parent contents are
outside cleanup ownership.

Before any payload is written, an unmounted Docker volume records the absolute
private root, workspace, and Compose project in labels. Labels contain no payload.
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
A failed cleanup returns an error and retains the record for retry. Other
workspaces' records and private directories remain outside that snapshot. CLI
`down` consults this runtime ownership before loading mutable configuration.

Behavioral evidence is separate from option-registration coverage:

- `TestUserDataFolderReadConfigurationHandler` proves retained feature staging
  with in-memory empty Docker discovery.
- `TestDockerComposeObjectCleanupCLI` proves real process-boundary folder routing,
  container-visible values, down without the creation flag, malformed and absent
  configuration, cleanup failure/retry, idempotence, and parent sentinels.
- `TestComposeObjectRemoveIntegration` proves host Remove for default and selected
  parents, lost containers, changed configuration, file-source preservation and
  independent workspace isolation.
- `TestComposeObjectPublicationIntegration` holds ownership publication while a
  second writer completes; the first then succeeds or fails. It proves private
  output, retained ownership on cleanup failure and a fresh-host retry.
- `TestComposeObjectCreationFailureIntegration` proves cleanup after real
  container creation failure; `TestComposeObjectInterruptedProcessIntegration`
  proves cleanup after killing the producer process.
- `TestComposeObjectLedgerRemovalRetryIntegration` proves retry when a real
  Docker container holds the ownership volume.
- `TestObjectMaterializerErrorsDoNotDisclosePayload` proves local filesystem
  errors contain none of the individually injected payload spellings.
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
retain after-success publication. System patch markers and session probe caches
have no current dcb writer. `Resolver.CacheDir` has no current cache reader/writer.

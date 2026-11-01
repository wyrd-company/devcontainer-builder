---
relationships:
  references: [release, recover-release, channels, receipts, recovery, config]
---

# Release publication

Intentional creates the release records from the reviewed release plan. The
bare code tag starts the Release workflow. The Go alias identifies the same
commit and does not start another build.

## Build and publish

The compatibility job proves the package-publisher contract. The build job
builds, stages and verifies all outputs, then retains the complete `dist`
bundle for 90 days. It also retains the separate package handoff manifest.

The publication job downloads those exact bytes. A failed publication job can
be rerun with **Re-run failed jobs**. The successful build and compatibility
jobs do not run again. Publication verifies existing versions and refuses to
replace different bytes.

npm publication preflights every package, publishes missing native packages
and the launcher, then checks registry visibility. Missing metadata is polled
for up to ten minutes. A different integrity or an authorization failure stops
immediately. The job logs npm acceptance separately from byte verification.
Accepted identities and their integrities are saved as a workflow artifact,
including when verification fails. A job retry restores those receipts and
verifies accepted packages without another publish request.

Homebrew runs after npm verification. Documentation and package-repository
submission follow a successful Release workflow. A docs tag on the code
release commit defers to that workflow; a docs-only tag publishes separately.

## Recover an existing release

Use **Recover release publication** for a final GitHub release whose build
outputs are already public:

```sh
gh workflow run recover-release.yml -f version=1.2.3
```

Recovery downloads the original archives, packages, checksums and manifest.
It checks out the original release tag for packaging metadata and the npm
launcher. The tag, manifest and assets must agree. It repacks npm tarballs
and renders Homebrew from the original binaries; it does not compile, move
release tags or replace GitHub assets. Already published npm bytes must match.

Recovery uses the same npm visibility checks and saved acceptance receipts.
Rerun its failed job to retain those receipts. A new manual recovery run has
its own receipt artifact. Check the successful run and the public registry,
then verify the installed CLI version and the corrected behavior.

Recovery publishes npm and Homebrew. The original Release workflow remains
the source of the retained downstream handoff. Documentation can also be
published through its manual workflow with the release tag as `ref`.

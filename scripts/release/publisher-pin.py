#!/usr/bin/env python3
# relationships:
#   references: [release, submit-package, manifest]
"""Keep publication blocked until the approved package publisher commit is set."""

import os

from manifest import COMMIT, version_value

version_value(os.environ["RELEASE_VERSION"])
if not COMMIT.fullmatch(os.environ["REPO_WYRD_FOO_PUBLISHER_COMMIT"]):
    raise SystemExit(
        "Replace PUBLISHER_COMMIT_UNSET after Bob merges the dcb publisher entry"
    )

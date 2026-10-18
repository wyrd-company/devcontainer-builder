#!/usr/bin/env python3
# relationships:
#   references: [manifest, config]
"""Bind publication to Intentional's committed npm version and exact bare tag."""

import json
import os
import subprocess
from pathlib import Path

from manifest import version_value

version = version_value(os.environ["RELEASE_VERSION"])
if os.environ.get("GITHUB_REF_TYPE") != "tag":
    raise SystemExit("publication requires a tag push")
head = subprocess.check_output(["git", "rev-parse", "HEAD"], text=True).strip()
tag_head = subprocess.check_output(
    ["git", "rev-parse", f"refs/tags/{version}^{{}}"], text=True
).strip()
if head != tag_head:
    raise SystemExit("release tag must identify the checked-out commit")
if json.loads(Path("npm/package.json").read_text())["version"] != version:
    raise SystemExit(
        "npm version must match the tag; Bob must release through Intentional"
    )
if subprocess.check_output(
    ["git", "status", "--porcelain", "--untracked-files=no"], text=True
):
    raise SystemExit("release source must be clean")

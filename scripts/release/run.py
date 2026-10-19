#!/usr/bin/env python3
# relationships:
#   references: [manifest, release]
"""Resolve and verify the successful Release run used by downstream workflows."""

import json
import os
import re
import subprocess
import sys
from pathlib import Path

from manifest import COMMIT, verify_handoff
from git_env import git_env


def run_identity(metadata, repository):
    if (
        metadata["path"] != ".github/workflows/release.yml"
        or metadata["event"] != "push"
        or metadata["conclusion"] != "success"
        or metadata["head_repository"]["full_name"] != repository
        or not COMMIT.fullmatch(metadata["head_sha"])
    ):
        raise ValueError(
            "expected a successful same-repository Release tag-push workflow run"
        )
    return metadata["head_sha"]


def resolve():
    run_id = os.environ["RELEASE_RUN_ID"]
    if not re.fullmatch(r"[1-9][0-9]*", run_id):
        raise ValueError("release run ID must be a positive integer")
    repository = os.environ["GITHUB_REPOSITORY"]
    metadata = json.loads(
        subprocess.check_output(
            ["gh", "api", f"repos/{repository}/actions/runs/{run_id}"]
        )
    )
    commit = run_identity(metadata, repository)
    with Path(os.environ["GITHUB_OUTPUT"]).open("a") as output:
        output.write(f"run_id={run_id}\nhead_sha={commit}\n")


def verify():
    commit = os.environ["RELEASE_COMMIT"]
    version = verify_handoff(
        "handoff/release-manifest.json", commit, os.environ.get("EXPECTED_TAG") or None
    )
    subprocess.run(
        ["git", "fetch", "--no-tags", "origin", f"refs/tags/{version}"],
        check=True, env=git_env(),
    )
    tag_commit = subprocess.check_output(
        ["git", "rev-parse", "FETCH_HEAD^{}"], text=True, env=git_env()
    ).strip()
    if tag_commit != commit:
        raise ValueError(
            "retained handoff, release run and tag must identify one commit"
        )
    with Path(os.environ["GITHUB_OUTPUT"]).open("a") as output:
        output.write(f"version={version}\n")


if __name__ == "__main__":
    {"resolve": resolve, "verify": verify}[sys.argv[1]]()

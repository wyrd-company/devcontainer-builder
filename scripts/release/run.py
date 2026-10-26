#!/usr/bin/env python3
# relationships:
#   references: [manifest, release]
"""Resolve and verify the successful Release run used by downstream workflows,
and choose the ref the docs publish uses."""

import json
import os
import re
import subprocess
import sys
from pathlib import Path

from manifest import COMMIT, verify_handoff, version_value
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


def docs_target(event, ref_name, version, input_ref, code_tags_at_head):
    """Return (ref, publish) for the docs publish of one workflow event.

    A Release run publishes its verified tag. A docs record alone publishes
    itself; pushed with a code tag, it waits for that tag's Release run, because
    the product page needs the GitHub Release. A manual run publishes its ref.
    """
    if event == "workflow_run":
        return version_value(version), True
    if event == "workflow_dispatch":
        if not input_ref:
            raise ValueError("a manual run needs a ref")
        return input_ref, True
    if event == "push" and re.fullmatch(r"dcb-docs@[0-9][^/\s]*", ref_name):
        return ref_name, not code_tags_at_head
    raise ValueError(f"no docs publish for {event} {ref_name}")


def target():
    code_tags = subprocess.check_output(
        ["git", "tag", "--list", "[0-9]*.[0-9]*.[0-9]*", "--points-at", "HEAD"],
        text=True, env=git_env(),
    ).split()
    ref, publish = docs_target(
        os.environ["EVENT_NAME"],
        os.environ.get("REF_NAME", ""),
        os.environ.get("RELEASE_VERSION", ""),
        os.environ.get("INPUT_REF", ""),
        code_tags,
    )
    with Path(os.environ["GITHUB_OUTPUT"]).open("a") as output:
        output.write(f"ref={ref}\npublish={str(publish).lower()}\n")


if __name__ == "__main__":
    {"resolve": resolve, "verify": verify, "target": target}[sys.argv[1]]()

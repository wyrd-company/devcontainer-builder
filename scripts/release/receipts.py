#!/usr/bin/env python3
# relationships:
#   references: [channels, release]
"""Restore accepted npm publication receipts from this workflow run."""
import json
import os
import shutil
import subprocess
import tempfile
from pathlib import Path


def restore(dist):
    repository = os.environ["GITHUB_REPOSITORY"]
    run_id = os.environ["GITHUB_RUN_ID"]
    artifacts = json.loads(subprocess.check_output(
        ["gh", "api", f"repos/{repository}/actions/runs/{run_id}/artifacts?per_page=100"], text=True
    ))["artifacts"]
    matches = [a for a in artifacts if a["name"] == "npm-publication-receipts" and not a["expired"]]
    if not matches:
        return
    if len(matches) != 1:
        raise ValueError("ambiguous npm publication receipts")
    with tempfile.TemporaryDirectory(prefix="npm-receipts-") as directory:
        subprocess.run(["gh", "run", "download", run_id, "-R", repository,
                        "--name", "npm-publication-receipts", "--dir", directory], check=True)
        source = Path(directory) / "npm-publication-receipts.json"
        # The publisher validates the release identity and every integrity before writes.
        dist.mkdir(exist_ok=True)
        shutil.copyfile(source, dist / source.name)


if __name__ == "__main__":
    restore(Path("dist"))

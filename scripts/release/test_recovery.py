# relationships:
#   validates: [recovery, receipts, recover-release]
import json
import os
import runpy
import shutil
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

import manifest
import receipts
import recovery
from test_release import ROOT, VERSION, COMMIT, fixture, yaml_file


class RecoveryTests(unittest.TestCase):
    def setUp(self):
        self.root = Path(self.enterContext(tempfile.TemporaryDirectory()))
        self.dist = self.root / "dist"
        self.dist.mkdir()
        fixture(self.dist)
        self.record = manifest.create(self.dist, VERSION, COMMIT)
        (self.dist / "release-manifest.json").write_text(json.dumps(self.record))
        self.source = self.root / "source"
        self.source.mkdir()

    def test_original_source_and_handoff_are_preserved_while_repacking(self):
        previous = recovery.stage.ROOT
        def stage(dist, version, commit):
            self.assertEqual(recovery.stage.ROOT, self.source.resolve())
            self.assertEqual((dist, version, commit), (self.dist, VERSION, COMMIT))
        with patch.object(recovery.subprocess, "check_output", side_effect=[COMMIT+"\n", COMMIT+"\n", ""]) as git, patch.object(recovery.stage, "stage", side_effect=stage):
            recovery.prepare(self.dist, VERSION, self.source)
        self.assertEqual(recovery.stage.ROOT, previous)
        self.assertEqual(json.loads((self.dist / "release-manifest.json").read_text()), self.record)
        for call in git.call_args_list:
            self.assertFalse(any(k.startswith("GIT_") for k in call.kwargs["env"]))

    def test_wrong_tag_dirty_source_and_changed_assets_fail_before_packaging(self):
        for outputs, message in [([COMMIT+"\n", "b"*40+"\n", ""], "requested release tag"),
                                 ([COMMIT+"\n", COMMIT+"\n", " M README.md\n"], "must be clean")]:
            with self.subTest(message=message), patch.object(recovery.subprocess, "check_output", side_effect=outputs), patch.object(recovery.stage, "stage") as stage:
                with self.assertRaisesRegex(ValueError, message):
                    recovery.prepare(self.dist, VERSION, self.source)
                stage.assert_not_called()
        (self.dist / manifest.assets(VERSION)[0][-1]).write_bytes(b"changed")
        with patch.object(recovery.subprocess, "check_output", side_effect=[COMMIT+"\n", COMMIT+"\n", ""]), patch.object(recovery.stage, "stage") as stage:
            with self.assertRaisesRegex(ValueError, "checksum"):
                recovery.prepare(self.dist, VERSION, self.source)
            stage.assert_not_called()

    def test_download_validates_the_original_complete_release(self):
        expected = {name for *_,name in manifest.assets(VERSION)} | {"checksums.txt", "release-manifest.json"}
        release = {"draft":False,"prerelease":False,"tag_name":VERSION,"assets":[{"name":name} for name in expected]}
        target = self.root / "download"
        def download(args, **kwargs):
            self.assertEqual(args[:4], ["gh", "release", "download", VERSION])
            for name in expected:
                shutil.copyfile(self.dist / name, target / name)
        with patch.object(recovery.channels, "github_release", return_value=release), patch.object(recovery.subprocess, "run", side_effect=download):
            self.assertEqual(recovery.download(target, VERSION), COMMIT)
        self.assertEqual(json.loads((target / "release-manifest.json").read_text()), self.record)

    def test_failed_repack_restores_tooling_source(self):
        previous = recovery.stage.ROOT
        with patch.object(recovery.subprocess, "check_output", side_effect=[COMMIT+"\n", COMMIT+"\n", ""]), patch.object(recovery.stage, "stage", side_effect=RuntimeError("pack failed")):
            with self.assertRaisesRegex(RuntimeError, "pack failed"):
                recovery.prepare(self.dist, VERSION, self.source)
        self.assertEqual(recovery.stage.ROOT, previous)

    def test_download_rejects_unpublished_or_incomplete_release(self):
        for release in [None, {"draft":True}, {"draft":False,"prerelease":True},
                        {"draft":False,"prerelease":False,"tag_name":VERSION,"assets":[]}]:
            with self.subTest(release=release), patch.object(recovery.channels, "github_release", return_value=release), patch.object(recovery.subprocess, "run") as download:
                with self.assertRaises(ValueError):
                    recovery.download(self.root / "download", VERSION)
                download.assert_not_called()

    def test_recovery_workflow_does_not_build_or_change_release_tags(self):
        workflow = yaml_file(ROOT / ".github/workflows/recover-release.yml")
        steps = workflow["jobs"]["publish"]["steps"]
        runs = [s["run"] for s in steps if "run" in s]
        self.assertEqual(runs, ["task release:recover-download", "task release:recover-prepare", "task release:restore-receipts", "task release:recover-npm", "task release:recover-homebrew"])
        self.assertFalse(any("goreleaser" in s.get("uses", "") or "setup-go" in s.get("uses", "") for s in steps))
        checkouts = [s for s in steps if s.get("uses", "").startswith("actions/checkout@")]
        self.assertEqual(checkouts[0]["with"]["ref"], "${{ github.workflow_sha }}")
        self.assertEqual(checkouts[1]["with"]["ref"], "refs/tags/${{ inputs.version }}")
        self.assertLess(next(i for i,s in enumerate(steps) if s.get("run") == "task release:recover-download"), steps.index(checkouts[1]))

    def test_npm_entrypoint_does_not_attest_the_tooling_commit(self):
        arguments = ["recovery.py", "npm", "--version", VERSION, "--dist", str(self.dist), "--source", str(self.source)]
        with patch.object(sys, "argv", arguments), patch.object(recovery.subprocess, "check_output", return_value=COMMIT+"\n"), patch.object(recovery.channels, "publish_npm") as publish:
            runpy.run_path(str(ROOT / "scripts/release/recovery.py"), run_name="__main__")
        publish.assert_called_once_with(self.dist, VERSION, provenance=False)


class ReceiptRestoreTests(unittest.TestCase):
    def setUp(self):
        self.root = Path(self.enterContext(tempfile.TemporaryDirectory()))
        self.enterContext(patch.dict(os.environ, {"GITHUB_REPOSITORY":"example/project", "GITHUB_RUN_ID":"123"}))

    def test_missing_or_expired_receipts_need_no_download(self):
        for artifacts in [[], [{"name":"npm-publication-receipts","expired":True}]]:
            with patch.object(receipts.subprocess, "check_output", return_value=json.dumps({"artifacts":artifacts})), patch.object(receipts.subprocess, "run") as download:
                receipts.restore(self.root / "dist")
                download.assert_not_called()

    def test_receipts_are_restored_from_the_current_run(self):
        record = {"version":VERSION,"accepted":{}}
        def download(args, **kwargs):
            self.assertIn("123", args)
            destination = Path(args[args.index("--dir")+1])
            (destination / "npm-publication-receipts.json").write_text(json.dumps(record))
        artifacts = [{"name":"npm-publication-receipts","expired":False}]
        with patch.object(receipts.subprocess, "check_output", return_value=json.dumps({"artifacts":artifacts})), patch.object(receipts.subprocess, "run", side_effect=download):
            receipts.restore(self.root / "dist")
        self.assertEqual(json.loads((self.root / "dist/npm-publication-receipts.json").read_text()), record)

    def test_registry_or_artifact_errors_are_not_missing_receipts(self):
        with patch.object(receipts.subprocess, "check_output", side_effect=subprocess.CalledProcessError(1,"gh")):
            with self.assertRaises(subprocess.CalledProcessError):
                receipts.restore(self.root / "dist")

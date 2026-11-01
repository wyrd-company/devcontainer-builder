# relationships:
#   validates: [manifest, stage, check-tag, publish, run, preflight]
import copy
import importlib.util
import io
import json
import os
import runpy
import subprocess
import tarfile
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

from git_env import git_env

import manifest
import stage

ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location(
    "release_run", ROOT / "scripts/release/run.py"
)
release_run = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(release_run)
VERSION = "1.2.3"
COMMIT = "a" * 40


def yaml_file(path):
    return json.loads(subprocess.check_output(["yq", "-o=json", ".", str(path)]))


def fixture(root):
    for _, fmt, _, _, name in manifest.assets(VERSION):
        if fmt == "tar.gz":
            with tarfile.open(root / name, "w:gz") as stream:
                for entry, data in [
                    ("dcb", b"#!/bin/sh\necho example\n"),
                    ("LICENSE", b"license"),
                    ("README.md", b"readme"),
                ]:
                    info = tarfile.TarInfo(entry)
                    info.size = len(data)
                    info.mode = 0o755 if entry == "dcb" else 0o644
                    stream.addfile(info, io.BytesIO(data))
        else:
            (root / name).write_bytes(b"package")
    checksums(root)


def checksums(root):
    (root / "checksums.txt").write_text(
        "".join(
            f"{manifest.sha256(p)}  {p.name}\n"
            for p in sorted(root.iterdir())
            if p.is_file() and p.name.endswith((".tar.gz", ".deb", ".rpm"))
        )
    )


class ManifestTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        fixture(self.root)

    def test_linux_handoff_covers_six_assets_and_actual_digests(self):
        handoff = manifest.create(self.root, VERSION, COMMIT)
        self.assertEqual(len(handoff["artifacts"]), 6)
        self.assertEqual({item["os"] for item in handoff["artifacts"]}, {"linux"})
        self.assertEqual(
            handoff["source"], {"repository": manifest.REPOSITORY, "commit": COMMIT}
        )
        for item in handoff["artifacts"]:
            self.assertEqual(
                item["sha256"], manifest.sha256(self.root / item["filename"])
            )
            self.assertEqual(
                item["url"],
                f"https://github.com/{manifest.REPOSITORY}/releases/download/{VERSION}/{item['filename']}",
            )
        config = yaml_file(ROOT / ".goreleaser.yaml")["nfpms"][0]
        self.assertEqual(config["package_name"], handoff["package"]["name"])
        for key in ["description", "homepage", "maintainer", "license"]:
            self.assertEqual(config[key], handoff["package"][key])

    def test_rejects_missing_and_unexpected_assets(self):
        target = self.root / manifest.assets(VERSION)[0][4]
        target.rename(self.root / "unexpected.tar.gz")
        with self.assertRaisesRegex(ValueError, "asset matrix"):
            manifest.create(self.root, VERSION, COMMIT)

    def test_rejects_tampered_asset(self):
        (self.root / manifest.assets(VERSION)[0][4]).write_bytes(b"tampered")
        with self.assertRaisesRegex(ValueError, "checksum mismatch"):
            manifest.create(self.root, VERSION, COMMIT)

    def test_rejects_missing_duplicate_and_extra_checksums(self):
        path = self.root / "checksums.txt"
        original = path.read_text()
        for content in [
            "\n".join(original.splitlines()[1:]) + "\n",
            original + original.splitlines()[0] + "\n",
            original + "0" * 64 + "  unexpected.tar.gz\n",
        ]:
            with self.subTest(content=content[-80:]):
                path.write_text(content)
                with self.assertRaises(ValueError):
                    manifest.create(self.root, VERSION, COMMIT)

    def test_rejects_nonstable_versions_and_incomplete_commits(self):
        for value in ["v1.2.3", "1.2.3-rc.1", "01.2.3", "1.2", "1.2.3\n"]:
            with self.subTest(value=value), self.assertRaises(ValueError):
                manifest.version_value(value)
        with self.assertRaises(ValueError):
            manifest.create(self.root, VERSION, "abc")

    def test_handoff_binds_version_tag_product_repository_and_commit(self):
        original = manifest.create(self.root, VERSION, COMMIT)
        path = self.root / "release-manifest.json"
        path.write_text(json.dumps(original))
        self.assertEqual(manifest.verify_handoff(path, COMMIT, VERSION), VERSION)
        for key, value in [
            ("version", "v1.2.3"),
            ("tag", "9.9.9"),
            ("product", "example"),
            ("source", {"repository": "example/project", "commit": COMMIT}),
            ("source", {"repository": manifest.REPOSITORY, "commit": "b" * 40}),
        ]:
            with self.subTest(key=key):
                changed = copy.deepcopy(original)
                changed[key] = value
                path.write_text(json.dumps(changed))
                with self.assertRaises(ValueError):
                    manifest.verify_handoff(path, COMMIT, VERSION)
        path.write_text(json.dumps(original))
        with self.assertRaises(ValueError):
            manifest.verify_handoff(path, COMMIT, "9.9.9")

    def test_formula_covers_four_verified_archives(self):
        stage.render_formula(
            self.root, VERSION, manifest.verify_assets(self.root, VERSION)
        )
        formula = (self.root / "dcb.rb").read_text()
        for osname, _, suffix, _ in manifest.TARGETS:
            name = f"dcb_{VERSION}_{osname}_{suffix}.tar.gz"
            self.assertIn(name, formula)
            self.assertIn(manifest.sha256(self.root / name), formula)
        self.assertIn('bin.install "dcb"', formula)
        self.assertIn("#{bin}/dcb version", formula)

    def test_npm_native_metadata_and_binary_bytes_match_each_target(self):
        with patch("subprocess.run") as pack:
            packages = stage.stage_npm(self.root, VERSION)
        self.assertEqual(len(packages), 5)
        self.assertEqual(pack.call_count, 5)
        for package, (osname, _, suffix, nodearch) in zip(packages, manifest.TARGETS):
            metadata = json.loads((package / "package.json").read_text())
            self.assertEqual(metadata["name"], f"@wyrd-company/dcb-{osname}-{nodearch}")
            self.assertEqual(metadata["version"], VERSION)
            self.assertEqual(metadata["os"], [osname])
            self.assertEqual(metadata["cpu"], [nodearch])
            with tarfile.open(
                self.root / f"dcb_{VERSION}_{osname}_{suffix}.tar.gz"
            ) as archive:
                self.assertEqual(
                    (package / "bin/dcb").read_bytes(),
                    archive.extractfile("dcb").read(),
                )
            self.assertTrue((package / "bin/dcb").stat().st_mode & 0o111)
        main = json.loads((packages[-1] / "package.json").read_text())
        self.assertEqual(main["version"], VERSION)
        self.assertTrue((packages[-1] / "LICENSE-APACHE-2.0").is_file())
        self.assertTrue((packages[-1] / "NOTICE").is_file())
        self.assertEqual(
            main["optionalDependencies"],
            {
                f"@wyrd-company/dcb-{osname}-{nodearch}": VERSION
                for osname, _, _, nodearch in manifest.TARGETS
            },
        )
        self.assertEqual(
            (packages[-1] / "bin/dcb.js").read_bytes(),
            (ROOT / "npm/bin/dcb.js").read_bytes(),
        )

    def test_npm_rejects_nonregular_binary(self):
        archive = self.root / f"dcb_{VERSION}_linux_x86_64.tar.gz"
        with tarfile.open(archive, "w:gz") as stream:
            info = tarfile.TarInfo("dcb")
            info.type = tarfile.SYMTYPE
            info.linkname = "LICENSE"
            info.size = 7
            stream.addfile(info)
            license_info = tarfile.TarInfo("LICENSE")
            license_info.size = 7
            stream.addfile(license_info, io.BytesIO(b"license"))
        with (
            patch("subprocess.run"),
            self.assertRaisesRegex(ValueError, "regular dcb binary"),
        ):
            stage.stage_npm(self.root, VERSION)


class PublicationTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.bin = self.root / "bin"
        self.bin.mkdir()
        self.log = self.root / "calls.jsonl"
        # Commands are recording fakes. No publication command reaches a real CLI.
        for name in ["python3", "gh", "npm", "git", "goreleaser"]:
            path = self.bin / name
            path.write_text("""#!/usr/bin/python3
import json, os, sys
from pathlib import Path
name = Path(sys.argv[0]).name
with open(os.environ['COMMAND_LOG'], 'a') as output:
    output.write(json.dumps([name] + sys.argv[1:]) + '\\n')
if name == 'python3':
    key = 'CHANNEL_STATUS' if 'channels.py' in sys.argv[1] else 'GUARD_STATUS'
    sys.exit(int(os.environ.get(key, '0')))
if name == 'git' and 'diff' in sys.argv:
    sys.exit(int(os.environ.get('DIFF_STATUS', '1')))
""")
            path.chmod(0o755)
        (self.root / "dist").mkdir()
        (self.root / "dist/dcb.rb").write_text("formula")
        (self.root / "dist/release-notes.md").write_text("notes")
        (self.root / "scripts/release").mkdir(parents=True)
        (self.root / "scripts/release/notes.sh").write_text("echo notes")
        self.env = {
            **os.environ,
            "PATH": f"{self.bin}:{os.environ['PATH']}",
            "COMMAND_LOG": str(self.log),
            "RELEASE_VERSION": VERSION,
        }

    def execute(self, *args):
        return subprocess.run(
            ["bash", str(ROOT / "scripts/release/publish.sh"), *args],
            cwd=self.root,
            env=self.env,
            capture_output=True,
            text=True,
        )

    def calls(self):
        return [json.loads(line) for line in self.log.read_text().splitlines()]

    def test_guard_failure_stops_every_publication_channel(self):
        self.env["GUARD_STATUS"] = "1"
        for channel in ["github", "npm", "homebrew"]:
            with self.subTest(channel=channel):
                self.log.unlink(missing_ok=True)
                self.assertNotEqual(self.execute(channel, "tap").returncode, 0)
                self.assertEqual(
                    self.calls(), [["python3", "scripts/release/check-tag.py"]]
                )

    def test_shell_dispatches_npm_through_the_idempotent_channel_helper(self):
        self.assertEqual(self.execute("npm").returncode, 0)
        self.assertEqual(
            self.calls(),
            [
                ["python3", "scripts/release/check-tag.py"],
                ["python3", "scripts/release/channels.py", "npm"],
            ],
        )

    def test_release_build_task_checks_tag_before_nonpublishing_builder_and_stager(
        self,
    ):
        import shutil

        shutil.copyfile(ROOT / "taskfile.yml", self.root / "taskfile.yml")
        result = subprocess.run(
            ["task", "release:build"],
            cwd=self.root,
            env=self.env,
            capture_output=True,
            text=True,
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.calls()[0], ["python3", "scripts/release/check-tag.py"])
        self.assertEqual(
            self.calls()[1], ["goreleaser", "release", "--clean", "--skip=publish"]
        )
        self.assertEqual(
            self.calls()[-1][:3], ["python3", "scripts/release/stage.py", "--version"]
        )
        self.log.unlink()
        result = subprocess.run(
            ["task", "release:build"],
            cwd=self.root,
            env={**self.env, "GUARD_STATUS": "1"},
            capture_output=True,
        )
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.calls(), [["python3", "scripts/release/check-tag.py"]])

    def test_release_notes_require_intentional_changelog_section(self):
        (self.root / "CHANGELOG.md").write_text(
            "# Changelog\n\n## 1.2.3\n\n- Example change.\n\n## 1.2.2\n\n- Older change.\n"
        )
        command = ["bash", str(ROOT / "scripts/release/notes.sh")]
        result = subprocess.run(
            command + [VERSION], cwd=self.root, capture_output=True, text=True
        )
        self.assertEqual(result.returncode, 0)
        self.assertEqual(result.stdout.strip(), "- Example change.")
        result = subprocess.run(
            command + ["9.9.9"], cwd=self.root, capture_output=True, text=True
        )
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("no section", result.stderr)

    def test_shell_dispatches_github_through_the_idempotent_channel_helper(self):
        self.assertEqual(self.execute("github").returncode, 0)
        self.assertEqual(
            self.calls(),
            [
                ["python3", "scripts/release/check-tag.py"],
                ["python3", "scripts/release/channels.py", "github"],
            ],
        )

    def test_homebrew_dispatches_through_shared_publisher(self):
        self.assertEqual(self.execute("homebrew", "tap").returncode, 0)
        self.assertEqual(self.calls(), [["python3", "scripts/release/check-tag.py"], ["python3", "scripts/release/channels.py", "homebrew-publish", "tap"]])

    def test_homebrew_comparison_failure_stops_copy_commit_and_push(self):
        self.env["CHANNEL_STATUS"] = "1"
        self.assertNotEqual(self.execute("homebrew", "tap").returncode, 0)
        self.assertFalse((self.root / "tap").exists())
        self.assertEqual(
            self.calls(),
            [
                ["python3", "scripts/release/check-tag.py"],
                ["python3", "scripts/release/channels.py", "homebrew-publish", "tap"],
            ],
        )

    def test_credential_preflight_requires_each_declared_secret(self):
        workflow = yaml_file(ROOT / ".github/workflows/release.yml")
        step = next(
            step
            for step in workflow["jobs"]["release"]["steps"]
            if step.get("run") == "task release:preflight"
        )
        env = {**self.env, **{name: "example-value" for name in step["env"]}}
        command = ["bash", str(ROOT / "scripts/release/preflight.sh")]
        self.assertEqual(
            subprocess.run(
                command, cwd=self.root, env=env, capture_output=True
            ).returncode,
            0,
        )
        for key in step["env"]:
            with self.subTest(key=key):
                result = subprocess.run(
                    command,
                    cwd=self.root,
                    env={**env, key: ""},
                    capture_output=True,
                    text=True,
                )
                self.assertNotEqual(result.returncode, 0)
                self.assertIn(key, result.stderr)


class IdentityTests(unittest.TestCase):
    def test_tag_guard_binds_tag_type_commit_npm_version_and_clean_source(self):
        def execute(tag_type="tag", tag=COMMIT, version=VERSION, dirty=""):
            def git(args, **kwargs):
                if args[1] == "status":
                    return dirty
                return (COMMIT if args[-1] == "HEAD" else tag) + "\n"

            with (
                patch.dict(
                    os.environ,
                    {"RELEASE_VERSION": VERSION, "GITHUB_REF_TYPE": tag_type},
                ),
                patch("subprocess.check_output", side_effect=git),
                patch(
                    "pathlib.Path.read_text",
                    return_value=json.dumps({"version": version}),
                ),
            ):
                runpy.run_path(str(ROOT / "scripts/release/check-tag.py"))

        execute()
        for kwargs in [
            dict(tag_type="branch"),
            dict(tag="b" * 40),
            dict(version="9.9.9"),
            dict(dirty=" M source"),
        ]:
            with self.subTest(kwargs=kwargs), self.assertRaises(SystemExit):
                execute(**kwargs)

    def test_run_identity_rejects_foreign_failed_or_nonrelease_runs(self):
        metadata = dict(
            path=".github/workflows/release.yml",
            event="push",
            conclusion="success",
            head_repository={"full_name": manifest.REPOSITORY},
            head_sha=COMMIT,
        )
        self.assertEqual(
            release_run.run_identity(metadata, manifest.REPOSITORY), COMMIT
        )
        for key, value in [
            ("path", ".github/workflows/ci.yml"),
            ("event", "pull_request"),
            ("conclusion", "failure"),
            ("head_repository", {"full_name": "example/project"}),
            ("head_sha", "abc"),
        ]:
            with self.subTest(key=key), self.assertRaises(ValueError):
                release_run.run_identity({**metadata, key: value}, manifest.REPOSITORY)

    def test_resolve_validates_run_id_and_emits_validated_run_identity(self):
        metadata = dict(
            path=".github/workflows/release.yml",
            event="push",
            conclusion="success",
            head_repository={"full_name": manifest.REPOSITORY},
            head_sha=COMMIT,
        )
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory) / "output"
            env = {
                "RELEASE_RUN_ID": "123",
                "GITHUB_REPOSITORY": manifest.REPOSITORY,
                "GITHUB_OUTPUT": str(output),
            }
            with (
                patch.dict(os.environ, env),
                patch.object(
                    release_run.subprocess,
                    "check_output",
                    return_value=json.dumps(metadata).encode(),
                ) as lookup,
            ):
                release_run.resolve()
                lookup.assert_called_once_with(
                    ["gh", "api", f"repos/{manifest.REPOSITORY}/actions/runs/123"]
                )
                self.assertEqual(output.read_text(), f"run_id=123\nhead_sha={COMMIT}\n")
            for value in ["0", "-1", "1\n", "1/../other"]:
                with (
                    self.subTest(value=value),
                    patch.dict(os.environ, {**env, "RELEASE_RUN_ID": value}),
                    patch.object(
                        release_run.subprocess,
                        "check_output",
                        return_value=json.dumps(metadata).encode(),
                    ) as lookup,
                ):
                    with self.assertRaisesRegex(ValueError, "positive integer"):
                        release_run.resolve()
                    lookup.assert_not_called()

    def test_retained_handoff_fetches_exact_tag_and_checks_peeled_commit(self):
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory) / "output"
            env = {
                "RELEASE_COMMIT": COMMIT,
                "EXPECTED_TAG": VERSION,
                "GITHUB_OUTPUT": str(output),
            }
            with (
                patch.dict(os.environ, env),
                patch.object(release_run, "verify_handoff", return_value=VERSION),
                patch.object(release_run.subprocess, "run") as fetch,
                patch.object(
                    release_run.subprocess, "check_output", return_value=COMMIT + "\n"
                ),
            ):
                release_run.verify()
                fetch.assert_called_once_with(
                    ["git", "fetch", "--no-tags", "origin", f"refs/tags/{VERSION}"],
                    check=True,
                    env=git_env(),
                )
                self.assertEqual(output.read_text(), f"version={VERSION}\n")
            with (
                patch.dict(os.environ, env),
                patch.object(release_run, "verify_handoff", return_value=VERSION),
                patch.object(release_run.subprocess, "run"),
                patch.object(
                    release_run.subprocess, "check_output", return_value="b" * 40 + "\n"
                ),
            ):
                with self.assertRaisesRegex(ValueError, "identify one commit"):
                    release_run.verify()

    def test_workflows_share_retained_artifact_and_run_identity(self):
        release = yaml_file(ROOT / ".github/workflows/release.yml")
        build = release["jobs"]["build"]["steps"]
        steps = release["jobs"]["release"]["steps"]
        self.assertEqual(release["jobs"]["build"]["needs"], "compatibility")
        self.assertEqual(release["jobs"]["release"]["needs"], "build")
        for gate in ["task release:build", "task release:verify"]:
            self.assertTrue(any(s.get("run") == gate for s in build))
            self.assertFalse(any(s.get("run") == gate for s in steps))
        self.assertFalse(any("goreleaser" in s.get("uses", "") or "setup-go" in s.get("uses", "") for s in steps))
        producer = next(s for s in build if s.get("with", {}).get("name") == "package-release-manifest")
        bundle = next(s for s in build if s.get("with", {}).get("name") == "release-bundle")
        consumer = next(s for s in steps if s.get("with", {}).get("name") == "release-bundle")
        self.assertEqual(bundle["with"]["path"], "dist/")
        self.assertEqual(consumer["with"]["path"], "dist")
        self.assertLess(steps.index(consumer), next(i for i,s in enumerate(steps) if s.get("run") == "task release:github"))
        receipt = next(s for s in steps if s.get("with", {}).get("name") == "npm-publication-receipts")
        self.assertEqual(receipt["if"], "always()")
        self.assertTrue(any(s.get("run") == "task release:restore-receipts" for s in steps))
        self.assertIs(producer["with"]["overwrite"], True)
        for filename in ["submit-package", "publish-docs"]:
            workflow = yaml_file(ROOT / f".github/workflows/{filename}.yml")
            self.assertEqual(workflow["permissions"], {})
            self.assertEqual(
                workflow["on"]["workflow_run"]["workflows"], [release["name"]]
            )
            steps = next(iter(workflow["jobs"].values()))["steps"]
            consumer = next(
                step
                for step in steps
                if step.get("uses", "").startswith("actions/download-artifact@")
            )
            self.assertEqual(consumer["with"]["name"], producer["with"]["name"])
            self.assertEqual(producer["with"]["path"], "dist/release-manifest.json")
            self.assertEqual(consumer["with"]["path"], "handoff")
            self.assertEqual(
                consumer["with"]["run-id"], "${{ steps.release_run.outputs.run_id }}"
            )
            self.assertLess(
                next(
                    i for i, s in enumerate(steps) if s.get("run") == "task release:run"
                ),
                steps.index(consumer),
            )
            self.assertLess(
                steps.index(consumer),
                next(
                    i
                    for i, s in enumerate(steps)
                    if s.get("run") == "task release:handoff"
                ),
            )
            self.assertEqual(
                next(
                    step for step in steps if step.get("run") == "task release:handoff"
                )["env"]["RELEASE_COMMIT"],
                "${{ steps.release_run.outputs.head_sha }}",
            )
        for filename in ["release", "release-check", "submit-package", "publish-docs", "recover-release"]:
            workflow = yaml_file(ROOT / f".github/workflows/{filename}.yml")
            self.assertEqual(workflow["permissions"], {})
            for job in workflow["jobs"].values():
                self.assertIn("permissions", job)
                if "uses" in job:
                    # A reusable-workflow call has no steps or timeout.
                    self.assertRegex(
                        job["uses"],
                        r"^wyrd-company/wyrd\.foo/\.github/workflows/publish-docs\.yml@[a-f0-9]{40}$",
                    )
                    self.assertNotEqual(job.get("secrets"), "inherit")
                    continue
                self.assertEqual(job["timeout-minutes"], 30)
                for step in job["steps"]:
                    if "uses" in step:
                        self.assertRegex(step["uses"], r"@[a-f0-9]{40}$")
                    if "run" in step:
                        self.assertTrue(step["run"].startswith("task "))


class DocsTargetTests(unittest.TestCase):
    def test_release_run_publishes_its_verified_tag(self):
        self.assertEqual(
            release_run.docs_target("workflow_run", "main", VERSION, "", []),
            (VERSION, True),
        )
        with self.assertRaises(ValueError):
            release_run.docs_target("workflow_run", "main", "v1.2.3", "", [])

    def test_manual_run_publishes_the_given_ref(self):
        self.assertEqual(
            release_run.docs_target("workflow_dispatch", "main", "", "dcb-docs@0.1.0", []),
            ("dcb-docs@0.1.0", True),
        )
        with self.assertRaises(ValueError):
            release_run.docs_target("workflow_dispatch", "main", "", "", [])

    def test_docs_record_publishes_alone_but_waits_for_a_code_tag(self):
        self.assertEqual(
            release_run.docs_target("push", "dcb-docs@0.1.1", "", "", []),
            ("dcb-docs@0.1.1", True),
        )
        self.assertEqual(
            release_run.docs_target("push", "dcb-docs@0.1.1", "", "", [VERSION]),
            ("dcb-docs@0.1.1", False),
        )

    def test_other_pushes_do_not_publish(self):
        for ref in ["main", VERSION, "dcb-docs@"]:
            with self.assertRaises(ValueError):
                release_run.docs_target("push", ref, "", "", [])

    def test_release_workflow_starts_only_on_bare_semver_tags(self):
        release = yaml_file(ROOT / ".github/workflows/release.yml")
        self.assertEqual(
            release["on"]["push"]["tags"], ["[0-9]*.[0-9]*.[0-9]*"]
        )

    def test_publish_workflow_has_no_main_push_trigger(self):
        triggers = yaml_file(ROOT / ".github/workflows/publish-docs.yml")["on"]
        self.assertEqual(triggers["push"], {"tags": ["dcb-docs@*"]})
        self.assertIn("workflow_dispatch", triggers)


if __name__ == "__main__":
    unittest.main()

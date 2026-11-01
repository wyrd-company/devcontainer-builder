# relationships:
#   validates: [channels, publish]
import base64
import hashlib
import io
import json
import os
import shutil
import subprocess
import sys
import tarfile
import tempfile
import unittest
from unittest.mock import patch
from pathlib import Path

import channels
import manifest
from test_release import ROOT, VERSION, fixture

# Recording CLI adapters have persistent fake remote state so the same real
# channel program can fail partway through, restart, and compare published bytes.
FAKE_CLI = """#!/usr/bin/python3
import base64, hashlib, json, os, shutil, sys, tarfile
from pathlib import Path
root = Path(os.environ['FAKE_REGISTRY'])
state = json.loads((root / 'state').read_text())
name, args = Path(sys.argv[0]).name, sys.argv[1:]
with (root / 'calls').open('a') as output:
    output.write(json.dumps([name] + args) + '\\n')
def save():
    (root / 'state').write_text(json.dumps(state))
def error(message):
    print(message, file=sys.stderr)
    save()
    sys.exit(1)
if name == 'gh':
    if args[0] == 'api':
        if state.get('github_error'):
            error('HTTP 403: forbidden')
        if state['release'] is None:
            error('HTTP 404: not found')
        print(json.dumps(state['release']))
    elif args[:2] == ['release', 'download']:
        filename = args[args.index('--pattern') + 1]
        shutil.copyfile(root / 'remote' / filename, Path(args[args.index('--dir') + 1]) / filename)
    elif args[:2] in [['release', 'create'], ['release', 'upload']]:
        if args[1] == 'create':
            state['release'] = dict(tag_name=args[2], draft=False, prerelease=False, assets=[])
            paths = args[args.index('--notes-file') + 2:]
        else:
            paths = args[3:]
        for index, filename in enumerate(paths):
            if state.get('github_fail_after') == index:
                error('simulated partial upload')
            path = Path(filename)
            shutil.copyfile(path, root / 'remote' / path.name)
            state['release']['assets'].append({'name': path.name})
        if state.get('github_drop_asset'):
            state['release']['assets'].pop()
        save()
    else:
        error('unexpected gh arguments')
elif name == 'npm':
    if args[0] == 'view':
        if state.get('npm_error'):
            print(json.dumps({'error': {'code': 'E403'}}))
            error('forbidden')
        if args[1] not in state['npm']:
            print(json.dumps({'error': {'code': 'E404'}}))
            error('not found')
        print(json.dumps(state['npm'][args[1]]))
    elif args[0] == 'publish':
        path = Path(args[1])
        with tarfile.open(path) as archive:
            metadata = json.load(archive.extractfile('package/package.json'))
        identity = metadata['name'] + '@' + metadata['version']
        if state.get('npm_fail') == identity:
            error('simulated npm failure')
        if identity in state['npm']:
            error('version already exists')
        state['npm'][identity] = 'sha512-' + base64.b64encode(hashlib.sha512(path.read_bytes()).digest()).decode()
        if state.get('npm_wrong_integrity'):
            state['npm'][identity] = 'sha512-different'
        save()
    else:
        error('unexpected npm arguments')
"""


class ChannelsTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        (self.root / "dist").mkdir()
        fixture(self.root / "dist")
        (self.root / "dist/release-manifest.json").write_text("{}")
        (self.root / "dist/release-notes.md").write_text("example notes")
        (self.root / "dist/dcb.rb").write_text(
            f'class Dcb < Formula\n  version "{VERSION}"\nend\n'
        )
        self.remote = self.root / "registry"
        (self.remote / "remote").mkdir(parents=True)
        self.state = {"release": None, "npm": {}}
        self.save()
        binaries = self.root / "bin"
        binaries.mkdir()
        for name in ["gh", "npm"]:
            (binaries / name).write_text(FAKE_CLI)
            (binaries / name).chmod(0o755)
        self.env = {
            **os.environ,
            "PATH": f"{binaries}:{os.environ['PATH']}",
            "FAKE_REGISTRY": str(self.remote),
            "RELEASE_VERSION": VERSION,
            "PYTHONDONTWRITEBYTECODE": "1",
        }
        self.names = [
            f"dcb-{osname}-{nodearch}" for osname, _, _, nodearch in manifest.TARGETS
        ] + ["dcb"]
        packs = self.root / "dist/npm/tarballs"
        packs.mkdir(parents=True)
        for name in self.names:
            with tarfile.open(
                packs / f"wyrd-company-{name}-{VERSION}.tgz", "w:gz"
            ) as archive:
                data = json.dumps(
                    {"name": f"@wyrd-company/{name}", "version": VERSION}
                ).encode()
                entry = tarfile.TarInfo("package/package.json")
                entry.size = len(data)
                archive.addfile(entry, io.BytesIO(data))

    def save(self):
        (self.remote / "state").write_text(json.dumps(self.state))

    def load(self):
        self.state = json.loads((self.remote / "state").read_text())

    def calls(self):
        return [
            json.loads(line)
            for line in (self.remote / "calls").read_text().splitlines()
        ]

    def execute(self, channel, *args):
        return subprocess.run(
            [sys.executable, str(ROOT / "scripts/release/channels.py"), channel, *args],
            cwd=self.root,
            env=self.env,
            capture_output=True,
            text=True,
        )

    def test_github_verifies_tag_and_uploads_handoff_with_packages(self):
        result = self.execute("github")
        self.assertEqual(result.returncode, 0, result.stderr)
        create = next(
            call for call in self.calls() if call[:3] == ["gh", "release", "create"]
        )
        self.assertEqual(create[3], VERSION)
        self.assertIn("--verify-tag", create)
        self.assertIn("--notes-file", create)
        for *_, filename in manifest.assets(VERSION):
            self.assertIn(f"dist/{filename}", create)
        self.assertIn("dist/release-manifest.json", create)
        self.assertIn("dist/checksums.txt", create)
        count = len(self.calls())
        self.assertEqual(self.execute("github").returncode, 0)
        self.assertFalse(
            any(
                c[:3] in [["gh", "release", "create"], ["gh", "release", "upload"]]
                for c in self.calls()[count:]
            )
        )

    def test_github_partial_upload_can_resume_and_existing_bytes_cannot_change(self):
        self.state["github_fail_after"] = 2
        self.save()
        self.assertNotEqual(self.execute("github").returncode, 0)
        self.load()
        del self.state["github_fail_after"]
        self.save()
        count = len(self.calls())
        result = self.execute("github")
        self.assertEqual(result.returncode, 0, result.stderr)
        upload = next(
            c for c in self.calls()[count:] if c[:3] == ["gh", "release", "upload"]
        )
        first_two = [name for *_, name in manifest.assets(VERSION)][:2]
        self.assertFalse(any(f"dist/{name}" in upload for name in first_two))
        (self.remote / "remote" / first_two[0]).write_bytes(b"different remote bytes")
        count = len(self.calls())
        result = self.execute("github")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("asset differs", result.stderr)
        self.assertFalse(
            any(
                c[:3] in [["gh", "release", "create"], ["gh", "release", "upload"]]
                for c in self.calls()[count:]
            )
        )

    def test_github_rejects_foreign_draft_duplicate_and_unexpected_assets(self):
        name = manifest.assets(VERSION)[0][-1]
        shutil.copyfile(self.root / "dist" / name, self.remote / "remote" / name)
        for change in [
            {"tag_name": "9.9.9"},
            {"draft": True},
            {"prerelease": True},
            {"assets": [{"name": manifest.assets(VERSION)[0][-1]}] * 2},
            {"assets": [{"name": "example"}]},
        ]:
            self.state["release"] = dict(
                tag_name=VERSION, draft=False, prerelease=False, assets=[]
            )
            self.state["release"].update(change)
            self.save()
            with self.subTest(change=change):
                result = self.execute("github")
                self.assertNotEqual(result.returncode, 0)
                if "assets" in change:
                    self.assertIn("duplicate or unexpected assets", result.stderr)
        self.assertFalse(
            any(
                c[:3] in [["gh", "release", "create"], ["gh", "release", "upload"]]
                for c in self.calls()
            )
        )

    def test_npm_publishes_four_native_tarballs_before_launcher_and_rerun_skips_all(
        self,
    ):
        result = self.execute("npm")
        self.assertEqual(result.returncode, 0, result.stderr)
        publishes = [c for c in self.calls() if c[:2] == ["npm", "publish"]]
        self.assertEqual(
            [c[2] for c in publishes],
            [
                f"dist/npm/tarballs/wyrd-company-{name}-{VERSION}.tgz"
                for name in self.names
            ],
        )
        self.assertTrue(
            all(c[-3:] == ["--access", "public", "--provenance"] for c in publishes)
        )
        count = len(self.calls())
        self.assertEqual(self.execute("npm").returncode, 0)
        self.assertFalse(any(c[:2] == ["npm", "publish"] for c in self.calls()[count:]))

    def test_npm_partial_publish_can_resume_and_conflict_preflight_prevents_writes(
        self,
    ):
        self.state["npm_fail"] = f"@wyrd-company/{self.names[1]}@{VERSION}"
        self.save()
        self.assertNotEqual(self.execute("npm").returncode, 0)
        self.load()
        self.assertEqual(len(self.state["npm"]), 1)
        del self.state["npm_fail"]
        self.save()
        count = len(self.calls())
        self.assertEqual(self.execute("npm").returncode, 0)
        published = [c for c in self.calls()[count:] if c[:2] == ["npm", "publish"]]
        self.assertEqual(len(published), 4)
        self.load()
        # A conflict in the last package must be discovered before writing any missing dependency.
        self.state["npm"] = {f"@wyrd-company/dcb@{VERSION}": "sha512-different"}
        self.save()
        count = len(self.calls())
        result = self.execute("npm")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("different bytes", result.stderr)
        self.assertFalse(any(c[:2] == ["npm", "publish"] for c in self.calls()[count:]))

    def test_npm_recovery_omits_provenance_and_still_verifies_all_bytes(self):
        with patch.dict(os.environ, self.env):
            channels.publish_npm(self.root / "dist", VERSION, provenance=False)
        publishes = [c for c in self.calls() if c[:2] == ["npm", "publish"]]
        self.assertEqual(len(publishes), 5)
        self.assertTrue(all("--provenance" not in c for c in publishes))
        self.load()
        for archive in (self.root / "dist/npm/tarballs").glob("*.tgz"):
            with tarfile.open(archive) as package:
                meta = json.load(package.extractfile("package/package.json"))
            identity = meta["name"] + "@" + meta["version"]
            self.assertEqual(self.state["npm"][identity], "sha512-" + base64.b64encode(hashlib.sha512(archive.read_bytes()).digest()).decode())

    def test_npm_publishes_all_before_waiting_and_retries_only_missing_metadata(self):
        clock = [0]
        accepted = {}
        events = []
        def view(identity):
            events.append(("view", identity))
            return accepted.get(identity) if clock[0] >= 10 else None
        def publish(args):
            self.assertEqual(args[:2], ["npm", "publish"])
            data = Path(args[2]).read_bytes()
            with tarfile.open(args[2]) as archive:
                meta = json.load(archive.extractfile("package/package.json"))
            identity = meta["name"] + "@" + meta["version"]
            self.assertNotIn(identity, accepted)
            accepted[identity] = "sha512-" + base64.b64encode(hashlib.sha512(data).digest()).decode()
            events.append(("publish", identity))
            return subprocess.CompletedProcess(args, 0, "accepted", "")
        def sleep(seconds):
            self.assertEqual(len(accepted), 5)
            clock[0] += seconds
        with patch.object(channels, "npm_integrity", side_effect=view), patch.object(channels, "command", side_effect=publish), patch.object(channels.time, "monotonic", side_effect=lambda: clock[0]), patch.object(channels.time, "sleep", side_effect=sleep):
            channels.publish_npm(self.root / "dist", VERSION)
        self.assertEqual(len([e for e in events if e[0] == "publish"]), 5)
        self.assertEqual(clock[0], 10)
        self.assertEqual([e[0] for e in events][5:10], ["publish"] * 5)
        # Even when metadata is absent again, receipts prevent another publish.
        clock[0] = 0
        with patch.object(channels, "npm_integrity", side_effect=view), patch.object(channels, "command", side_effect=AssertionError("must not republish accepted packages")), patch.object(channels.time, "monotonic", side_effect=lambda: clock[0]), patch.object(channels.time, "sleep", side_effect=sleep):
            channels.publish_npm(self.root / "dist", VERSION)

    def test_npm_visibility_timeout_is_bounded_and_receipts_survive(self):
        clock = [0]
        def sleep(seconds):
            clock[0] += seconds
        with patch.object(channels, "npm_integrity", return_value=None), patch.object(channels, "command", return_value=subprocess.CompletedProcess([], 0, "accepted", "")), patch.object(channels, "NPM_VISIBILITY_SECONDS", 20), patch.object(channels.time, "monotonic", side_effect=lambda: clock[0]), patch.object(channels.time, "sleep", side_effect=sleep):
            with self.assertRaisesRegex(ValueError, "metadata is still unavailable"):
                channels.publish_npm(self.root / "dist", VERSION)
        self.assertEqual(clock[0], 20)
        receipt = json.loads((self.root / "dist/npm-publication-receipts.json").read_text())
        self.assertEqual(len(receipt["accepted"]), 5)

    def test_receipt_conflict_prevents_any_publication(self):
        identity = f"@wyrd-company/dcb@{VERSION}"
        (self.root / "dist/npm-publication-receipts.json").write_text(json.dumps({"version": VERSION, "accepted": {identity: "sha512-different"}}))
        result = self.execute("npm")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("receipt has different bytes", result.stderr)
        self.assertFalse(any(c[:2] == ["npm", "publish"] for c in self.calls()))

    def test_homebrew_publisher_retries_push_when_formula_has_no_diff(self):
        tap = self.root / "tap"
        git_env = __import__("git_env").git_env
        remote = self.root / "example-remote.git"
        subprocess.run(["git", "init", "--bare", "--initial-branch=main", str(remote)], check=True, capture_output=True, env=git_env(fixture=True))
        subprocess.run(["git", "init", "--initial-branch=main", str(tap)], check=True, capture_output=True, env=git_env(fixture=True))
        subprocess.run(["git", "-C", str(tap), "remote", "add", "origin", str(remote)], check=True, env=git_env(fixture=True))
        (tap / "Formula").mkdir()
        (tap / "Formula/dcb.rb").write_text('version "0.0.1"\n')
        channels.publish_homebrew(self.root / "dist", VERSION, tap)
        self.assertEqual((tap / "Formula/dcb.rb").read_bytes(), (self.root / "dist/dcb.rb").read_bytes())
        first = subprocess.check_output(["git", "-C", str(tap), "rev-parse", "HEAD"], text=True, env=git_env()).strip()
        channels.publish_homebrew(self.root / "dist", VERSION, tap)
        self.assertEqual(subprocess.check_output(["git", "-C", str(remote), "rev-parse", "main"], text=True, env=git_env()).strip(), first)

    def test_command_failures_with_valid_json_are_not_success(self):
        with self.assertRaises(RuntimeError):
            channels.require_success(
                subprocess.CompletedProcess([], 1, "{}", "example failure")
            )

    def test_github_refuses_local_asset_tamper_before_lookup(self):
        (self.root / "dist" / manifest.assets(VERSION)[0][-1]).write_bytes(b"changed")
        result = self.execute("github")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("checksum mismatch", result.stderr)
        self.assertFalse((self.remote / "calls").exists())

    def test_github_refuses_incomplete_successful_upload(self):
        self.state["github_drop_asset"] = True
        self.save()
        result = self.execute("github")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("still missing", result.stderr)

    def test_npm_tarball_identity_mismatch_prevents_registry_calls(self):
        path = (
            self.root / f"dist/npm/tarballs/wyrd-company-{self.names[0]}-{VERSION}.tgz"
        )
        for change in [{"name": "@example/package"}, {"version": "9.9.9"}]:
            metadata = {
                "name": f"@wyrd-company/{self.names[0]}",
                "version": VERSION,
                **change,
            }
            data = json.dumps(metadata).encode()
            with tarfile.open(path, "w:gz") as archive:
                entry = tarfile.TarInfo("package/package.json")
                entry.size = len(data)
                archive.addfile(entry, io.BytesIO(data))
            with self.subTest(change=change):
                result = self.execute("npm")
                self.assertNotEqual(result.returncode, 0)
                self.assertIn("identity disagrees", result.stderr)
                self.assertFalse((self.remote / "calls").exists())

    def test_npm_verifies_integrity_after_publish(self):
        self.state["npm_wrong_integrity"] = True
        self.save()
        result = self.execute("npm")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("different bytes", result.stderr)

    def test_registry_errors_are_not_treated_as_missing_versions(self):
        for channel, flag in [("github", "github_error"), ("npm", "npm_error")]:
            self.state[flag] = True
            self.save()
            with self.subTest(channel=channel):
                self.assertNotEqual(self.execute(channel).returncode, 0)
        self.assertFalse(
            any(
                c[:3] in [["gh", "release", "create"], ["gh", "release", "upload"]]
                or c[:2] == ["npm", "publish"]
                for c in self.calls()
            )
        )

    def test_homebrew_matching_and_older_versions_can_resume_but_same_version_conflict_fails(
        self,
    ):
        tap = self.root / "tap"
        (tap / "Formula").mkdir(parents=True)
        formula = tap / "Formula/dcb.rb"
        shutil.copyfile(self.root / "dist/dcb.rb", formula)
        self.assertEqual(self.execute("homebrew", str(tap)).returncode, 0)
        formula.write_text(f'version "{VERSION}"\n# different formula\n')
        self.assertNotEqual(self.execute("homebrew", str(tap)).returncode, 0)
        formula.write_text('version "0.0.1"\n')
        self.assertEqual(self.execute("homebrew", str(tap)).returncode, 0)

    def test_homebrew_newer_version_fails_before_copy_or_git_writes(self):
        tap = self.root / "tap"
        (tap / "Formula").mkdir(parents=True)
        formula = tap / "Formula/dcb.rb"
        for version in ["1.2.4", "1.10.0", "2.0.0"]:
            with self.subTest(version=version):
                original = f'version "{version}"\n# existing formula\n'
                formula.write_text(original)
                with patch.object(channels.subprocess, "run", return_value=subprocess.CompletedProcess([], 0, "", "")) as git:
                    with self.assertRaisesRegex(ValueError, "refusing to downgrade Homebrew"):
                        channels.publish_homebrew(self.root / "dist", VERSION, tap)
                    git.assert_not_called()
                self.assertEqual(formula.read_text(), original)

    def test_homebrew_unknown_version_fails_before_copy_or_git_writes(self):
        tap = self.root / "tap"
        (tap / "Formula").mkdir(parents=True)
        formula = tap / "Formula/dcb.rb"
        for original in ['# no explicit version\n', 'version "invalid"\n']:
            with self.subTest(formula=original):
                formula.write_text(original)
                with patch.object(channels.subprocess, "run", return_value=subprocess.CompletedProcess([], 0, "", "")) as git:
                    with self.assertRaises(ValueError):
                        channels.publish_homebrew(self.root / "dist", VERSION, tap)
                    git.assert_not_called()
                self.assertEqual(formula.read_text(), original)


if __name__ == "__main__":
    unittest.main()

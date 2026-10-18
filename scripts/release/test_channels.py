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
                self.assertNotEqual(self.execute("github").returncode, 0)
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
        self.assertIn("does not yet match", result.stderr)

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


if __name__ == "__main__":
    unittest.main()

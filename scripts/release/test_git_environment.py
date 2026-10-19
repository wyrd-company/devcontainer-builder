# relationships:
#   validates: [git-env, git_env, publish, run]
import contextlib
import os
import subprocess
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

from git_env import git_env
import run as release_run
from test_release import ROOT, VERSION


class ReleaseGitEnvironmentTests(unittest.TestCase):
    def setUp(self):
        self.root = Path(self.enterContext(tempfile.TemporaryDirectory()))
        self.sentinel = self.root / "sentinel"
        self.git("init", "-q", "-b", "main", str(self.sentinel))
        (self.sentinel / "example.txt").write_text("sentinel\n")
        self.git("-C", str(self.sentinel), "add", ".")
        self.git("-C", str(self.sentinel), "commit", "-q", "-m", "Sentinel")
        self.origin = self.root / "origin.git"
        self.git("clone", "-q", "--bare", str(self.sentinel), str(self.origin))
        # Even a defective push can reach only this test-owned local remote.
        self.git("-C", str(self.sentinel), "remote", "add", "origin", str(self.origin))
        self.before = self.snapshot()
        self.addCleanup(self.assert_sentinel_unchanged)
        home = self.root / "home"
        home.mkdir()
        self.enterContext(patch.dict(os.environ, {
            "HOME": str(home),
            "XDG_CONFIG_HOME": str(home / ".config"),
            "GIT_DIR": str(self.sentinel / ".git"),
            "GIT_WORK_TREE": str(self.sentinel),
        }))

    def git(self, *args):
        return subprocess.check_output(["git", *args], env=git_env(fixture=True), text=True).strip()

    def snapshot(self):
        return {
            str(p.relative_to(self.root)): p.read_bytes()
            for root in [self.sentinel, self.origin]
            for p in root.rglob("*") if p.is_file()
        }

    def assert_sentinel_unchanged(self):
        self.assertEqual(self.before, self.snapshot(), "sentinel repository or remote changed")

    def test_git_env_removes_all_git_overrides(self):
        overrides = {
            name: "inherited" for name in [
                "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY", "GIT_COMMON_DIR",
                "GIT_CONFIG_COUNT", "GIT_CONFIG_KEY_0", "GIT_CONFIG_VALUE_0",
                "GIT_NAMESPACE", "GIT_FUTURE_OVERRIDE", "GIT_AUTHOR_NAME",
            ]
        }
        with patch.dict(os.environ, {**overrides, "EXAMPLE_KEEP": "value=with=equals"}):
            env = git_env(fixture=True)
            release_env = git_env()
        self.assertFalse(any(name.startswith("GIT_") for name in release_env))
        for name in overrides:
            self.assertNotEqual(env.get(name), "inherited", name)
        self.assertNotIn("GIT_DIR", env)
        self.assertNotIn("GIT_WORK_TREE", env)
        self.assertEqual(env["GIT_AUTHOR_NAME"], "Example")
        self.assertEqual(env["GIT_COMMITTER_EMAIL"], "example@example.invalid")
        self.assertEqual(env["EXAMPLE_KEEP"], "value=with=equals")

    def test_publish_ignores_inherited_repository(self):
        tap = self.root / "tap"
        self.git("clone", "-q", str(self.origin), str(tap))
        remote = self.root / "tap.git"
        self.git("clone", "-q", "--bare", str(tap), str(remote))
        self.git("-C", str(tap), "remote", "set-url", "origin", str(remote))
        (self.root / "dist").mkdir()
        (self.root / "dist/dcb.rb").write_text("example formula\n")
        tools = self.root / "bin"
        tools.mkdir()
        # Only tag/channel checks are stubbed. All Git operations are real and
        # both possible remotes are local repositories owned by this test.
        python = tools / "python3"
        python.write_text("#!/bin/sh\nexit 0\n")
        python.chmod(0o755)
        result = subprocess.run(
            ["bash", str(ROOT / "scripts/release/publish.sh"), "homebrew", str(tap)],
            cwd=self.root,
            env={**os.environ, "PATH": str(tools) + os.pathsep + os.environ["PATH"],
                 "RELEASE_VERSION": VERSION},
            capture_output=True, text=True,
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assert_sentinel_unchanged()
        self.assertEqual(
            self.git("--git-dir=" + str(remote), "show", "main:Formula/dcb.rb"),
            "example formula",
        )
        # Publication retains the configured release identity.
        self.assertEqual(
            self.git("-C", str(tap), "log", "-1", "--format=%an"), "dcb release bot"
        )

    def test_handoff_fetch_ignores_inherited_repository(self):
        self.git("--git-dir=" + str(self.origin), "tag", VERSION)
        self.before = self.snapshot()
        checkout = self.root / "checkout"
        self.git("clone", "-q", str(self.origin), str(checkout))
        commit = self.git("-C", str(checkout), "rev-parse", "HEAD")
        with (
            contextlib.chdir(checkout),
            patch.dict(os.environ, {
                "RELEASE_COMMIT": commit,
                "EXPECTED_TAG": VERSION,
                "GITHUB_OUTPUT": str(self.root / "output"),
            }),
            patch.object(release_run, "verify_handoff", return_value=VERSION),
        ):
            release_run.verify()
        self.assert_sentinel_unchanged()
        self.assertTrue((checkout / ".git/FETCH_HEAD").is_file())
        self.assertEqual((self.root / "output").read_text(), f"version={VERSION}\n")

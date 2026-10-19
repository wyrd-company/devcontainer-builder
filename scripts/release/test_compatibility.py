# relationships:
#   validates: [compatibility, publisher-pin, release, submit-package]
import json
import os
import runpy
import subprocess
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

from test_release import ROOT, VERSION, yaml_file
from git_env import git_env



class CompatibilityTests(unittest.TestCase):
    def assert_publisher_pins(self, gate_pin, action_pin):
        self.assertRegex(action_pin, r"^[a-f0-9]{40}$")
        # The gate deliberately blocks release until the product entry is approved.
        # Activation must select the same immutable publisher as the submit action.
        if gate_pin != "PUBLISHER_COMMIT_UNSET":
            self.assertRegex(gate_pin, r"^[a-f0-9]{40}$")
            self.assertEqual(gate_pin, action_pin)

    def test_publisher_action_pin_matches_activated_gate(self):
        release = yaml_file(ROOT / ".github/workflows/release.yml")
        submit = yaml_file(ROOT / ".github/workflows/submit-package.yml")
        gate_pin = release["jobs"]["compatibility"]["env"][
            "REPO_WYRD_FOO_PUBLISHER_COMMIT"
        ]
        action = next(
            step["uses"]
            for step in submit["jobs"]["submit"]["steps"]
            if step.get("uses", "").startswith("wyrd-company/repo.wyrd.foo/")
        )
        self.assert_publisher_pins(gate_pin, action.split("@", 1)[1])

    def test_publisher_pin_agreement_accepts_blocked_and_matching_fixtures(self):
        for gate_pin in ["PUBLISHER_COMMIT_UNSET", "a" * 40]:
            with self.subTest(gate_pin=gate_pin):
                self.assert_publisher_pins(gate_pin, "a" * 40)

    def test_publisher_pin_agreement_rejects_mismatching_and_non_sha_fixtures(self):
        for gate_pin, action_pin in [
            ("a" * 40, "b" * 40),
            ("a" * 40, "PUBLISHER_COMMIT_UNSET"),
            ("PUBLISHER_COMMIT_UNSET", "PUBLISHER_COMMIT_UNSET"),
            ("invalid", "a" * 40),
        ]:
            with self.subTest(gate_pin=gate_pin, action_pin=action_pin):
                with self.assertRaises(AssertionError):
                    self.assert_publisher_pins(gate_pin, action_pin)

    def test_unset_pin_blocks_before_publisher_checkout_and_both_workflows_agree(self):
        release = yaml_file(ROOT / ".github/workflows/release.yml")
        submit = yaml_file(ROOT / ".github/workflows/submit-package.yml")["jobs"][
            "submit"
        ]
        job = release["jobs"]["compatibility"]
        pin = job["env"]["REPO_WYRD_FOO_PUBLISHER_COMMIT"]
        action = next(
            s
            for s in submit["steps"]
            if s.get("uses", "").startswith("wyrd-company/repo.wyrd.foo/")
        )
        self.assertEqual(submit["env"]["REPO_WYRD_FOO_PUBLISHER_COMMIT"], pin)
        self.assertEqual(release["jobs"]["release"]["needs"], "compatibility")
        submit_gate = next(
            s for s in submit["steps"] if s.get("run") == "task release:publisher-pin"
        )
        self.assertLess(
            submit["steps"].index(submit_gate), submit["steps"].index(action)
        )
        self.assertEqual(
            submit_gate["env"]["RELEASE_VERSION"],
            "${{ steps.handoff.outputs.version }}",
        )
        steps = job["steps"]
        gate = next(s for s in steps if s.get("run") == "task release:publisher-pin")
        checkout = next(
            s
            for s in steps
            if s.get("with", {}).get("repository") == "wyrd-company/repo.wyrd.foo"
        )
        self.assertLess(steps.index(gate), steps.index(checkout))
        self.assertEqual(
            checkout["with"]["ref"], "${{ env.REPO_WYRD_FOO_PUBLISHER_COMMIT }}"
        )
        self.assertEqual(
            next(s for s in steps if s.get("run") == "task release:compatibility")[
                "env"
            ]["PUBLISHER_ROOT"],
            checkout["with"]["path"],
        )
        for value in ["PUBLISHER_COMMIT_UNSET", "", "abc", "A" * 40]:
            with (
                self.subTest(value=value),
                patch.dict(
                    os.environ,
                    {
                        "RELEASE_VERSION": VERSION,
                        "REPO_WYRD_FOO_PUBLISHER_COMMIT": value,
                    },
                ),
            ):
                with self.assertRaises(SystemExit):
                    runpy.run_path(str(ROOT / "scripts/release/publisher-pin.py"))
        with patch.dict(
            os.environ,
            {"RELEASE_VERSION": VERSION, "REPO_WYRD_FOO_PUBLISHER_COMMIT": "a" * 40},
        ):
            runpy.run_path(str(ROOT / "scripts/release/publisher-pin.py"))

    def test_compatibility_runs_complete_local_contract_and_rejects_wrong_checkout(
        self,
    ):
        directory = self.enterContext(tempfile.TemporaryDirectory())
        with self.subTest("inherited Git repository"):
            root = Path(directory)
            sentinel = root / "sentinel"
            subprocess.run(
                ["git", "init", "--quiet", str(sentinel)], check=True, env=git_env(fixture=True)
            )
            (sentinel / "example.txt").write_text("sentinel\n")
            for args in [["add", "."], ["commit", "--quiet", "-m", "Sentinel"]]:
                subprocess.run(
                    ["git", "-C", str(sentinel), *args], check=True, env=git_env(fixture=True)
                )

            def snapshot():
                return {
                    str(p.relative_to(sentinel)): p.read_bytes()
                    for p in sentinel.rglob("*") if p.is_file()
                }

            before = snapshot()

            def assert_sentinel_unchanged():
                self.assertEqual(before, snapshot(), "sentinel repository changed")

            # Cleanup checks the sentinel even if fixture setup or the script fails.
            self.addCleanup(assert_sentinel_unchanged)
            overrides = patch.dict(
                os.environ,
                {"GIT_DIR": str(sentinel / ".git"), "GIT_WORK_TREE": str(sentinel)},
            )
            overrides.start()
            self.addCleanup(overrides.stop)
            publisher = root / "publisher"
            (publisher / "scripts").mkdir(parents=True)
            (publisher / "config").mkdir()
            (publisher / "config/products.json").write_text("{}")
            (publisher / "pubkey.gpg").write_text("example public key")
            subprocess.run(
                ["git", "init", "--quiet", str(publisher)], check=True, env=git_env(fixture=True)
            )
            subprocess.run(
                [
                    "git",
                    "-C",
                    str(publisher),
                    "-c",
                    "user.name=Example",
                    "-c",
                    "user.email=example@example.invalid",
                    "commit",
                    "--quiet",
                    "--allow-empty",
                    "-m",
                    "Fixture",
                ],
                check=True,
                env=git_env(fixture=True),
            )
            commit = subprocess.check_output(
                ["git", "-C", str(publisher), "rev-parse", "HEAD"],
                text=True, env=git_env(fixture=True),
            ).strip()
            (publisher / "scripts/repository.py").write_text("""import json, os, sys
from pathlib import Path
with open(os.environ['CALL_LOG'], 'a') as out:
    out.write(json.dumps(sys.argv[1:]) + '\\n')
command = sys.argv[1]
if command == 'submit':
    reply = os.environ.get('SUBMISSION', 'created\t' + 'releases/dcb/1.2.3.json')
    relative = reply.split('\t')[1]
    target = Path(sys.argv[3]) / relative
    target.parent.mkdir(parents=True)
    target.write_text('{}')
    print(reply)
elif command == 'stage' and os.environ.get('SKIP_OUTPUT') != 'stage':
    (Path(sys.argv[4]) / '.new-rpms').write_text('example.rpm')
elif command == 'render-aur' and os.environ.get('SKIP_OUTPUT') != 'aur':
    (Path(sys.argv[3]) / 'PKGBUILD').write_text('example')
elif command == 'validate-queue' and os.environ.get('REJECT_QUEUE'):
    sys.exit(1)
""")
            log = root / "calls"
            env = {
                **os.environ,
                "CALL_LOG": str(log),
            }
            command = [
                "bash",
                str(ROOT / "scripts/release/compatibility.sh"),
                str(publisher),
                commit,
                "manifest.json",
                "assets",
                str(root / "output"),
            ]
            result = subprocess.run(command, env=env, capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            assert_sentinel_unchanged()
            inbox = next((root / "output").glob("contract.*/inbox"))
            subject = subprocess.check_output(
                ["git", "-C", str(inbox), "log", "-1", "--format=%s"],
                env=git_env(fixture=True), text=True,
            ).strip()
            self.assertEqual(subject, "Add package release manifest")
            calls = [json.loads(line) for line in log.read_text().splitlines()]
            self.assertEqual(
                [c[0] for c in calls],
                ["validate", "submit", "validate-queue", "stage", "render-aur"],
            )
            self.assertEqual(calls[2][-1], str(publisher / "config/products.json"))
            self.assertEqual(calls[2][3], "releases/dcb/1.2.3.json")
            for file in [
                publisher / "scripts/repository.py",
                publisher / "config/products.json",
                publisher / "pubkey.gpg",
            ]:
                saved = file.read_bytes()
                file.unlink()
                log.unlink(missing_ok=True)
                result = subprocess.run(
                    command, env=env, capture_output=True, text=True
                )
                self.assertNotEqual(result.returncode, 0)
                self.assertIn("does not expose", result.stderr)
                self.assertFalse(log.exists())
                file.write_bytes(saved)
            for output in ["stage", "aur"]:
                self.assertNotEqual(
                    subprocess.run(
                        command, env={**env, "SKIP_OUTPUT": output}, capture_output=True
                    ).returncode,
                    0,
                )
            for index, value in [(3, "b" * 40), (3, "PUBLISHER_COMMIT_UNSET")]:
                log.unlink(missing_ok=True)
                changed = command.copy()
                changed[index] = value
                self.assertNotEqual(
                    subprocess.run(changed, env=env, capture_output=True).returncode, 0
                )
                self.assertFalse(log.exists())
                if value == "PUBLISHER_COMMIT_UNSET":
                    result = subprocess.run(
                        changed, env=env, capture_output=True, text=True
                    )
                    self.assertIn("full commit SHA", result.stderr)
            for extra in [
                {"REJECT_QUEUE": "1"},
                {"SUBMISSION": "unchanged\treleases/dcb/1.2.3.json"},
                {"SUBMISSION": "created\treleases/example/1.2.3.json"},
            ]:
                log.unlink(missing_ok=True)
                self.assertNotEqual(
                    subprocess.run(
                        command, env={**env, **extra}, capture_output=True
                    ).returncode,
                    0,
                )
                calls = [json.loads(line)[0] for line in log.read_text().splitlines()]
                self.assertNotIn("stage", calls)


if __name__ == "__main__":
    unittest.main()

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


class CompatibilityTests(unittest.TestCase):
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
        self.assertEqual(action["uses"].split("@")[1], pin)
        self.assertEqual(submit["env"]["REPO_WYRD_FOO_PUBLISHER_COMMIT"], pin)
        self.assertEqual(release["jobs"]["release"]["needs"], "compatibility")
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
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            publisher = root / "publisher"
            (publisher / "scripts").mkdir(parents=True)
            (publisher / "config").mkdir()
            (publisher / "config/products.json").write_text("{}")
            (publisher / "pubkey.gpg").write_text("example public key")
            subprocess.run(["git", "init", "--quiet", str(publisher)], check=True)
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
            )
            commit = subprocess.check_output(
                ["git", "-C", str(publisher), "rev-parse", "HEAD"], text=True
            ).strip()
            (publisher / "scripts/repository.py").write_text("""import json, os, sys
from pathlib import Path
with open(os.environ['CALL_LOG'], 'a') as out:
    out.write(json.dumps(sys.argv[1:]) + '\\n')
command = sys.argv[1]
if command == 'submit':
    relative = 'releases/dcb/1.2.3.json'
    target = Path(sys.argv[3]) / relative
    target.parent.mkdir(parents=True)
    target.write_text('{}')
    print(os.environ.get('SUBMISSION', 'created\\t' + relative))
elif command == 'stage':
    (Path(sys.argv[4]) / '.new-rpms').write_text('example.rpm')
elif command == 'render-aur':
    (Path(sys.argv[3]) / 'PKGBUILD').write_text('example')
elif command == 'validate-queue' and os.environ.get('REJECT_QUEUE'):
    sys.exit(1)
""")
            log = root / "calls"
            env = {
                **os.environ,
                "CALL_LOG": str(log),
                "GIT_AUTHOR_NAME": "Example",
                "GIT_AUTHOR_EMAIL": "example@example.invalid",
                "GIT_COMMITTER_NAME": "Example",
                "GIT_COMMITTER_EMAIL": "example@example.invalid",
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
            calls = [json.loads(line) for line in log.read_text().splitlines()]
            self.assertEqual(
                [c[0] for c in calls],
                ["validate", "submit", "validate-queue", "stage", "render-aur"],
            )
            self.assertEqual(calls[2][-1], str(publisher / "config/products.json"))
            self.assertEqual(calls[2][3], "releases/dcb/1.2.3.json")
            for index, value in [(3, "b" * 40), (3, "PUBLISHER_COMMIT_UNSET")]:
                log.unlink(missing_ok=True)
                changed = command.copy()
                changed[index] = value
                self.assertNotEqual(
                    subprocess.run(changed, env=env, capture_output=True).returncode, 0
                )
                self.assertFalse(log.exists())
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

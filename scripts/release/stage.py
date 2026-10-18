#!/usr/bin/env python3
# relationships:
#   references: [manifest, dcb]
"""Stage local npm packages and a Homebrew formula from verified release assets."""

import argparse
import json
import shutil
import subprocess
import tarfile
from pathlib import Path

import manifest

ROOT = Path(__file__).resolve().parents[2]


def stage_npm(dist, version):
    output = dist / "npm"
    output.mkdir(exist_ok=True)
    main = output / "dcb"
    main.mkdir(exist_ok=True)
    metadata = json.loads((ROOT / "npm/package.json").read_text())
    metadata["version"] = version
    metadata["optionalDependencies"] = {}
    for osname, _, suffix, nodearch in manifest.TARGETS:
        name = f"dcb-{osname}-{nodearch}"
        package = output / name
        (package / "bin").mkdir(parents=True, exist_ok=True)
        archive = dist / f"dcb_{version}_{osname}_{suffix}.tar.gz"
        with tarfile.open(archive) as stream:
            # Extract only the binary, never archive paths or symlinks.
            members = [member for member in stream.getmembers() if member.name == "dcb"]
            if len(members) != 1 or not members[0].isfile() or members[0].size == 0:
                raise ValueError(
                    f"{archive} must contain exactly one regular dcb binary"
                )
            with (
                stream.extractfile(members[0]) as source,
                (package / "bin/dcb").open("wb") as dest,
            ):
                shutil.copyfileobj(source, dest)
        (package / "bin/dcb").chmod(0o755)
        scoped_name = f"@wyrd-company/{name}"
        platform_metadata = {
            key: metadata[key]
            for key in (
                "description",
                "homepage",
                "repository",
                "license",
                "publishConfig",
                "relationships",
            )
        }
        platform_metadata["license"] = manifest.PACKAGE["license"]
        platform_metadata.update(
            name=scoped_name,
            version=version,
            os=[osname],
            cpu=[nodearch],
            files=["bin/dcb", "LICENSE", "README.md"],
        )
        (package / "package.json").write_text(
            json.dumps(platform_metadata, indent=2) + "\n"
        )
        metadata["optionalDependencies"][scoped_name] = version
        for filename in ["LICENSE", "README.md"]:
            shutil.copyfile(ROOT / filename, package / filename)
    (main / "package.json").write_text(json.dumps(metadata, indent=2) + "\n")
    shutil.copytree(ROOT / "npm/bin", main / "bin", dirs_exist_ok=True)
    for filename in ["LICENSE", "README.md"]:
        shutil.copyfile(ROOT / filename, main / filename)
    for filename in ["LICENSE-APACHE-2.0", "NOTICE"]:
        shutil.copyfile(ROOT / "npm" / filename, main / filename)
    packs = output / "tarballs"
    packs.mkdir(exist_ok=True)
    # Pack native packages first; publish ordering uses the same package list.
    packages = [
        output / f"dcb-{osname}-{nodearch}"
        for osname, _, _, nodearch in manifest.TARGETS
    ] + [main]
    for package in packages:
        subprocess.run(
            [
                "npm",
                "pack",
                str(package.resolve()),
                "--ignore-scripts",
                "--json",
                "--userconfig=/dev/null",
                "--pack-destination",
                str(packs.resolve()),
            ],
            check=True,
        )
    return packages


def render_formula(dist, version, checksums):
    lines = [
        "# relationships:",
        "#   references: [manifest]",
        "class Dcb < Formula",
        f'  desc "{manifest.PACKAGE["description"]}"',
        f'  homepage "{manifest.PACKAGE["homepage"]}"',
        f'  version "{version}"',
        f'  license "{manifest.PACKAGE["license"]}"',
    ]
    for osname in ["darwin", "linux"]:
        lines.append(f"  on_{'macos' if osname == 'darwin' else 'linux'} do")
        for _, arch, suffix, _ in (t for t in manifest.TARGETS if t[0] == osname):
            filename = f"dcb_{version}_{osname}_{suffix}.tar.gz"
            lines.extend(
                [
                    f"    on_{'intel' if arch == 'amd64' else 'arm'} do",
                    f'      url "https://github.com/{manifest.REPOSITORY}/releases/download/{version}/{filename}"',
                    f'      sha256 "{checksums[filename]}"',
                    "    end",
                ]
            )
        lines.append("  end")
    lines.extend(
        [
            "  def install",
            '    bin.install "dcb"',
            "  end",
            "  test do",
            '    assert_match version.to_s, shell_output("#{bin}/dcb version")',
            "  end",
            "end",
            "",
        ]
    )
    (dist / "dcb.rb").write_text("\n".join(lines))


def stage(dist, version, commit):
    handoff = manifest.create(dist, version, commit)
    render_formula(dist, version, manifest.verify_assets(dist, version))
    stage_npm(dist, version)
    (dist / "release-manifest.json").write_text(json.dumps(handoff, indent=2) + "\n")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--dist", type=Path, default=Path("dist"))
    parser.add_argument("--version", required=True)
    parser.add_argument("--commit", required=True)
    args = parser.parse_args()
    stage(args.dist, args.version, args.commit)

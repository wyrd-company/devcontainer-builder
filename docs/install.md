---
title: Installation
order: 2
docs: true
install: true
relationships:
  references: [ manifest ]
---

`dcb` is one binary for Linux and macOS on `amd64` and `arm64`. It needs a
reachable Docker Engine at run time and nothing else. See
[Requirements](/docs/dcb/overview#requirements).

## Homebrew

On macOS and Linux:

```bash
brew install wyrd-company/tools/dcb
```

## npm

The npm package installs the release binary for the current platform. It
needs Node.js 18 or later.

```bash
npm install --global @wyrd-company/dcb
```

To run it once without a global install:

```bash
npx @wyrd-company/dcb --help
```

The binary comes from a platform package that npm installs as an optional
dependency. Do not disable optional dependencies for this package.

## APT

```bash
sudo install -d -m 0755 /etc/apt/keyrings
curl -fsSL https://repo.wyrd.foo/pubkey.gpg |
  sudo tee /etc/apt/keyrings/wyrd-company.gpg >/dev/null
echo "deb [signed-by=/etc/apt/keyrings/wyrd-company.gpg] \
https://repo.wyrd.foo/apt stable main" |
  sudo tee /etc/apt/sources.list.d/wyrd-company.list >/dev/null
sudo apt update
sudo apt install dcb
```

## RPM

```bash
sudo curl -fsSL https://repo.wyrd.foo/wyrd.repo \
  -o /etc/yum.repos.d/wyrd.repo
sudo dnf install dcb
```

## Arch User Repository

```bash
paru -S dcb-bin
```

DEB, RPM, and AUR packages install `/usr/bin/dcb`.

## Release archive

Each [GitHub release](https://github.com/wyrd-company/devcontainer-builder/releases)
has `dcb_<version>_<linux|darwin>_<x86_64|aarch64>.tar.gz` archives and a
`checksums.txt`. Each archive holds the binary, the license, and the README.

```bash
sha256sum --check --ignore-missing checksums.txt
tar -xzf dcb_*_linux_x86_64.tar.gz dcb
sudo install -m 0755 dcb /usr/local/bin/dcb
```

## From source

With Go installed:

```bash
go install github.com/wyrd-company/devcontainer-builder/cmd/dcb@latest
```

A binary built this way reports `dev` from `dcb version`. Release builds
report the release version.

## Check the installation

```bash
dcb version
```

```json
{"outcome":"success","version":"0.1.0"}
```

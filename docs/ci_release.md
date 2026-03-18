# CI & Release (GoReleaser + GitHub Actions)

## Why this matters
Automated releases in this repo currently:
- produce binaries for macOS/Linux/Windows
- produce Linux `.deb` and `.rpm` packages
- publish GitHub release artifacts and checksums through GoReleaser
- embed version metadata into the CLI binary
- publish a Homebrew cask when the tap repo token is configured
- generate and publish WinGet manifests when the fork token is configured

## Current GitHub Actions release flow
`.github/workflows/release.yml` (tag-triggered):
```yaml
name: Release
on:
  push:
    tags: ["v*.*.*"]

jobs:
  goreleaser:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0
      - uses: actions/setup-go@v5
        with:
          go-version: "1.24.x"
      - run: go test ./...
      - run: go vet ./...
      - uses: goreleaser/goreleaser-action@v6
        with: { version: "~> v2", args: release --clean }
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
          HOMEBREW_TAP_GITHUB_TOKEN: ${{ secrets.HOMEBREW_TAP_GITHUB_TOKEN }}
          WINGET_GITHUB_TOKEN: ${{ secrets.WINGET_GITHUB_TOKEN }}
```

## GoReleaser config (high-level)
- **Archives** for darwin/linux/windows, amd64/arm64
- **Windows archive format**: `zip`
- **macOS/Linux archive format**: `tar.gz`
- **Linux packages**: `.deb` and `.rpm`
- **Checksums**: `checksums.txt`
- **Release notes**: GitHub-based changelog

What is wired today:
- GitHub release artifacts
- Linux `.deb` / `.rpm` packages
- embedded version / commit / build date metadata
- checksums
- Homebrew cask generation and publishing to `RocketResearch-Inc/homebrew-tap`
- WinGet manifest generation and PR creation through `RocketResearch-Inc/winget-pkgs`

What is still external:
- creating `RocketResearch-Inc/homebrew-tap`
- creating `RocketResearch-Inc/winget-pkgs` as a fork of `microsoft/winget-pkgs`
- storing `HOMEBREW_TAP_GITHUB_TOKEN` and `WINGET_GITHUB_TOKEN` in repo secrets
- optional SBOM generation

This repository can now automate the build, archive, package, and publisher-update side. The remaining package-manager work is mostly repository ownership and credentials.

## Supported installer targets
- **Homebrew cask** (macOS): `brew tap RocketResearch-Inc/tap && brew install --cask compair`
- **WinGet** (Windows): `winget install RocketResearchInc.Compair`
- **Linux direct package install**: download the generated `.deb` or `.rpm` from GitHub Releases

For the exact one-time setup steps, see [Package Distribution Setup](package_distribution.md).

## Review CI

Release automation is separate from review automation.

For review/gating jobs, use:
- [CI Review Examples](ci_review_examples.md) for advisory vs blocking review runs
- a dedicated `COMPAIR_AUTH_TOKEN`
- committed `.compair/config.yaml` when the repo should keep syncing the same Compair document across ephemeral CI runners

![Release Flow](assets/ci_release.png)

# CI & Release (GoReleaser + GitHub Actions)

## Why this matters
Automated releases in this repo currently:
- produce binaries for macOS/Linux/Windows
- publish GitHub release artifacts and checksums through GoReleaser
- embed version metadata into the CLI binary

Package-manager publishing is not wired yet. Homebrew/Scoop/winget remain external follow-up work.

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
        with: { version: latest, args: release --clean }
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
```

## GoReleaser config (high-level)
- **Archives** for darwin/linux/windows, amd64/arm64
- **Windows archive format**: `zip`
- **macOS/Linux archive format**: `tar.gz`
- **Checksums**: `checksums.txt`
- **Release notes**: GitHub-based changelog

What is wired today:
- GitHub release artifacts
- embedded version / commit / build date metadata
- checksums

What is not wired today:
- Homebrew tap publishing
- Scoop bucket publishing
- winget publishing
- SBOM generation

This repository can automate the build-and-archive side. The package-manager targets still require:
- an owned tap/bucket repo
- credentials or GitHub token permissions to push there
- a release policy for winget PRs

## Potential later installer targets
- **Homebrew** (macOS/Linux): `brew install RocketResearch-Inc/tap/compair`
- **Scoop** (Windows): `scoop bucket add your-bucket ...; scoop install compair`
- **winget** (Windows): `winget install YourOrg.Compair`

## Review CI

Release automation is separate from review automation.

For review/gating jobs, use:
- [CI Review Examples](ci_review_examples.md) for advisory vs blocking review runs
- a dedicated `COMPAIR_AUTH_TOKEN`
- committed `.compair/config.yaml` when the repo should keep syncing the same Compair document across ephemeral CI runners

![Release Flow](assets/ci_release.png)

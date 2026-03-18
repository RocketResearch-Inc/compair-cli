# Package Distribution Setup

Use this guide when you want `compair-cli` releases to publish beyond raw GitHub archives.

The release workflow in this repo now handles:

- GitHub Releases with macOS, Linux, and Windows archives
- `checksums.txt`
- Linux `.deb` and `.rpm` packages
- Homebrew cask publishing when the tap repo and token exist
- WinGet manifest generation and PR creation when the fork repo and token exist

The remaining work is external setup: creating the publisher repos and storing the required GitHub secrets.

## 1. GitHub Releases

This path is already wired in `.github/workflows/release.yml` and `.goreleaser.yaml`.

What happens on a tag like `v1.2.3`:

- builds macOS archives for `amd64` and `arm64`
- builds Linux archives for `amd64` and `arm64`
- builds Windows archives for `amd64` and `arm64`
- generates `checksums.txt`
- generates Linux `.deb` and `.rpm` assets
- publishes everything to the GitHub Release for that tag

Release steps:

1. Cut and push a semantic version tag like `v1.2.3`.
2. Wait for the `Release` GitHub Actions workflow to finish.
3. Verify the Release page includes the expected archives, packages, and checksums.

Recommended smoke checks after each release:

- macOS/Linux: download an archive, unpack it, run `./compair version`
- Windows: download the `.zip`, unpack it, run `compair.exe version`
- Linux package check: install the generated `.deb` or `.rpm` on a clean VM and run `compair version`

## 2. Homebrew Cask (macOS)

This repo is configured to publish a cask to `RocketResearch-Inc/homebrew-tap`.

One-time setup:

1. Create a public repository named `homebrew-tap` under `RocketResearch-Inc`.
2. Leave the default branch as `main`.
3. Create a GitHub token with `Contents: Read and write` access to `RocketResearch-Inc/homebrew-tap`.
4. Add that token to `compair-cli` repo secrets as `HOMEBREW_TAP_GITHUB_TOKEN`.

What happens on the next tagged release:

- GoReleaser builds the macOS archives
- GoReleaser writes `Casks/compair.rb`
- GoReleaser commits the cask update to `RocketResearch-Inc/homebrew-tap`

How to test it:

```bash
brew tap RocketResearch-Inc/tap
brew install --cask compair
compair version
```

Notes:

- The generated cask is intended for the CLI binary, not a GUI app bundle.
- The cask includes a quarantine-removal hook because the release artifact is not notarized. If you later add signing/notarization, keep or simplify that hook based on actual install behavior.

## 3. WinGet (Windows)

This repo is configured to generate a WinGet manifest for package identifier `RocketResearchInc.Compair` and open a PR to `microsoft/winget-pkgs`.

One-time setup:

1. Fork `https://github.com/microsoft/winget-pkgs` into `RocketResearch-Inc/winget-pkgs`.
2. Create a GitHub token with `Contents: Read and write` and `Pull requests: Read and write` access to that fork.
3. Add that token to `compair-cli` repo secrets as `WINGET_GITHUB_TOKEN`.

What happens on the next tagged release:

- GoReleaser uses the Windows release archive
- GoReleaser generates the WinGet manifest files
- GoReleaser pushes a branch like `compair-1.2.3` to `RocketResearch-Inc/winget-pkgs`
- GoReleaser opens a PR from that fork to `microsoft/winget-pkgs:master`

How to test it:

1. Confirm the PR created by the release passes the WinGet repo checks.
2. On a Windows machine, use the manifest locally if you want a pre-merge smoke test.
3. After the PR merges, install with:

```powershell
winget install RocketResearchInc.Compair
```

Notes:

- If the PR automation is not ready, omit `WINGET_GITHUB_TOKEN`. GoReleaser will still generate the manifest into `dist/` without trying to publish it.
- If the upstream WinGet rules change, the fallback is to submit the generated manifest manually from `dist/`.

## 4. Linux `.deb` and `.rpm`

These packages are generated directly by GoReleaser through nFPM and attached to GitHub Releases. No external package repository is required for the first usable version.

What happens on a tagged release:

- GoReleaser builds Linux binaries
- nFPM generates `.deb` and `.rpm` packages
- the packages are uploaded to the GitHub Release alongside the archives

How to test them:

Debian/Ubuntu:

```bash
sudo apt install ./compair_<version>_linux_amd64.deb
compair version
```

Fedora/RHEL:

```bash
sudo dnf install ./compair-<version>-1.x86_64.rpm
compair version
```

Notes:

- This is enough for direct downloads from Releases.
- If you later want `apt install compair` or `dnf install compair` from a package repo, add a second distribution layer such as Cloudsmith, Gemfury, or your own apt/yum repository. That is separate from the release generation already configured here.

## Required GitHub Secrets

Set these in `compair-cli` repository settings:

| Secret | Required for | Notes |
| --- | --- | --- |
| `GITHUB_TOKEN` | GitHub Releases | Provided automatically by GitHub Actions |
| `HOMEBREW_TAP_GITHUB_TOKEN` | Homebrew cask publishing | Write access to `RocketResearch-Inc/homebrew-tap` |
| `WINGET_GITHUB_TOKEN` | WinGet PR publishing | Write access to `RocketResearch-Inc/winget-pkgs` fork and PR creation |

## Recommended Rollout Order

1. Verify GitHub Releases plus `.deb` / `.rpm` on one test tag.
2. Create and validate the Homebrew tap.
3. Create the WinGet fork and validate one PR submission.
4. After all three work once, add end-user install commands to the front-page README.

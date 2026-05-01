# Release Checklist (Compair CLI)

Use this as a pre-release checklist for beta/1.0 builds.

For the cross-product launch view and current validation status, see [Launch Readiness](launch_readiness.md).

## Build & packaging
- [ ] Run the `Release Dry Run` workflow and confirm `scripts/verify_release_artifacts.sh` passes
- [ ] Build binaries for macOS, Windows, Linux (amd64/arm64 as needed)
- [ ] Verify `go build` succeeds from a clean checkout
- [ ] Verify `compair version` reports the expected CLI version/commit
- [ ] Generate checksums for all artifacts
- [ ] Produce SBOMs if required (e.g., `syft` or `cargo about` equivalent for Go deps)
- [ ] Attach artifacts + checksums to the GitHub release
- [ ] Confirm Windows artifacts are zipped and macOS/Linux artifacts are `tar.gz`
- [ ] Confirm Linux `.deb` and `.rpm` packages are attached to the release
- [ ] Confirm the GitHub Pages Linux repo updated with the new packages and signed metadata

## API compatibility
- [ ] `login`, `whoami`, `signup` work against Cloud
- [ ] `group create`, `group ls`, `group use`, `track` verified
- [ ] `sync` baseline snapshot + diff flow verified
- [ ] `feedback` + `notes` endpoints verified
- [ ] `notifications` (Cloud) verified
- [ ] `chunk_mode=client` accepted by both Cloud and Core workers

## Core flows
- [ ] `core status`, `core doctor`, and `core up` verified on a clean machine
- [ ] `core logs` and `core restart` behave as expected
- [ ] `demo` works in both `--mode local` and `--mode cloud`
- [ ] Local Core no-key path is clearly caveated as functional but lower-fidelity than Cloud in setup-facing docs and CLI output
- [ ] Local Core with your own OpenAI key is smoke-tested and materially better than the bundled no-key path
- [ ] Seeded scenario results are captured in `docs/launch_validation.md`, including the recommendation that OpenAI-backed generation is the preferred local quality path
- [ ] First-time repo baseline snapshot works (no `last_synced_commit`)
- [ ] Subsequent sync uses diffs and updates `last_synced_commit`
- [ ] `review` runs a full review and writes the latest report
- [ ] `reports` renders the last report correctly
- [ ] `watch` runs without errors for at least 10 minutes
- [ ] `status` shows tracked items accurately

## UX & CLI behavior
- [ ] `--dry-run` outputs payload and exits cleanly
- [ ] `--debug-http` logs request details (status + request ID)
- [ ] Error messaging for missing active group is clear
- [ ] `compair --help` output includes new commands/flags

## Docs & onboarding
- [ ] `docs/user_guide.md` matches current commands
- [ ] `docs/index.md` quickstart works verbatim
- [ ] `docs/config_reference.md` includes profiles + snapshot caps
- [ ] `docs/ci_review_examples.md` matches the current review/report path
- [ ] Troubleshooting section updated (if applicable)
- [ ] README / launch-facing copy honestly positions Cloud as the strongest out-of-the-box experience and self-hosted Core as the bring-your-own-key path for higher review quality

## Cross-platform smoke tests
- [ ] macOS: login, init, sync, review
- [ ] Windows: login, init, sync, review
- [ ] Linux: login, init, sync, review
- [ ] Linux package smoke test: install the generated `.deb` and `.rpm`, then run `compair version`
- [ ] Linux repo smoke test: install via the Pages-backed APT or DNF repo, then run `compair version`

## Security & handling
- [ ] Credentials stored with correct permissions
- [ ] No tokens or secrets leaked in logs
- [ ] Validate permissions on `~/.compair/credentials.json`

## Observability
- [ ] HTTP debug logs are readable and include request IDs
- [ ] `compair stats` returns useful repo summary

## Release metadata
- [ ] Release notes created
- [ ] Version tags updated
- [ ] Links to docs and download instructions included

## Manual / external items
- [ ] Homebrew tap repo exists and release automation has write access
- [ ] `HOMEBREW_TAP_GITHUB_TOKEN` is configured in GitHub Actions secrets
- [ ] `RocketResearch-Inc/winget-pkgs` fork exists
- [ ] `WINGET_GITHUB_TOKEN` is configured in GitHub Actions secrets
- [ ] The current release created or updated the WinGet PR successfully
- [ ] `RocketResearch-Inc/compair-packages` exists and Pages serves `gh-pages`
- [ ] `LINUX_PACKAGES_GITHUB_TOKEN` is configured in GitHub Actions secrets
- [ ] `LINUX_REPO_GPG_PRIVATE_KEY` and `LINUX_REPO_GPG_PASSPHRASE` are configured in GitHub Actions secrets
- [ ] CI secrets configured (`COMPAIR_AUTH_TOKEN`, optional group IDs, PR comment tokens)
- [ ] If PR comments are enabled, repo permissions allow writing comments/status updates

Note:
- The current repo automation covers GitHub release artifacts, checksums, Linux packages, the GitHub Pages Linux package repo, Homebrew cask publication, and WinGet manifest generation.
- Pages, Homebrew, and WinGet still depend on external repos and signing or auth secrets. See [Package Distribution Setup](package_distribution.md).

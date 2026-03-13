# Deployment Guide (Maintainers)

This guide covers how to host the API base, configure environments, and publish the CLI.

## Environments
- **API Base URLs**
  - Dev: `http://localhost:4000`
  - Prod: `https://app.compair.sh/api`

Expose the API base to users via `COMPAIR_API_BASE` or `--api-base`.

## Secrets
- The CLI reads user tokens from `~/.compair/credentials.json`.
- The API should validate the `Authorization: Bearer` header.

## Publishing binaries (optional)
See [CI & Release](ci_release.md). With GitHub Actions + GoReleaser you can:
- Build macOS/Linux/Windows archives
- Publish Homebrew, Scoop, winget manifests once the external target repos exist
- Generate release notes and optional SBOMs

What this repo can automate directly:
- Building archives and checksums
- Embedding CLI version metadata
- Uploading GitHub release artifacts
- Running Compair review/gate jobs in CI

What still depends on external infrastructure:
- Homebrew tap ownership and credentials
- Scoop bucket ownership and credentials
- winget publishing path
- CI secrets for `COMPAIR_AUTH_TOKEN` and any PR comment integrations

## GitHub Pages (docs site)
- Put docs in `docs/` (already structured).
- Enable **Pages** in repo settings -> build from `main` branch `/docs`.
- (Optional) Edit `_config.yml` to change theme and navigation.

## Operational notes
- Monitor `/process_doc` task throughput; scale workers as needed.
- Consider webhooks from GitHub/GitLab to prewarm or accelerate processing (CLI doesn't require this).
- Add rate limiting and structured errors on the API; the CLI prints body text if a request fails.
- If CI should keep updating the same repo document, commit `.compair/config.yaml` so ephemeral runners reuse the existing binding.

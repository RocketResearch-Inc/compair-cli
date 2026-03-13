# Compair CLI (API-aligned)

Repository note: the shipped binary and product name are `compair` / "Compair CLI". In this workspace, the repository directory is `compair-cli`.

This build is aligned to your current API:
- Auth: `POST /login` → store token, send as `Authorization: Bearer` header.
- Groups: `/create_group`, `/load_groups`, `/load_group_users`, `/join_group`.
- Documents (repos): `/create_doc` to represent each repo; store `document_id` in `.compair/config.yaml`.
- Processing: `/process_doc` + `/status/{task_id}` used by `sync` and `watch`.
- First repo sync sends a baseline snapshot (file tree + full tracked text contents by default), then subsequent syncs use diffs.

## Build
```bash
go build -o compair .
```

For release automation, the current repository supports:

- GitHub Actions CI on macOS / Linux / Windows
- tag-triggered GitHub Releases through GoReleaser
- checksums and embedded version metadata

Package-manager publishing (Homebrew / Scoop / winget) is not wired yet.

## Quickstarts

### Cloud (SaaS)
```bash
# Use the hosted profile (default)
compair profile use cloud

# Create or invite team members
compair signup --email teammate@example.com --name "Teammate" --referral CODE123

# Login interactively.
# On Cloud, Compair will offer browser-based Google sign-in when available.
compair login

# Force browser-based sign-in explicitly
compair login browser

# Or use email/password directly
compair login --email you@example.com --password '...'

# Persist an existing token from web/device auth
compair login --token "$COMPAIR_AUTH_TOKEN"
compair demo
compair version
compair status
compair doctor
compair feedback-length detailed
compair whoami

# Workflows
compair group create "Platform Services"
compair group use grp_123
compair self-feedback on        # recommended for single-user cross-repo review
compair feedback-length detailed
compair track
compair review                  # run a full review and write .compair/latest_feedback_sync.md
compair reports                 # reopen saved reports later
compair notifications           # inspect ranked Cloud notification events directly
```

### Observability
```bash
compair activity
compair notifications --all-groups
compair docs list --filter recently_updated
```

When the target server is Compair Cloud and notification scoring is enabled, `compair review` and `compair sync` use those notification events as a report-ranking layer. On Core, the same commands still work, but there is no Cloud notification ranking workflow.

### Payload inspection
```bash
compair snapshot preview --output .compair/snapshot.md
compair diff --snapshot-mode auto
compair sync --json --gate api-contract
compair stats
```

### Core (Self-hosted)
```bash
# Inspect the local runtime config and Docker state
compair core status

# Start the default local Core container (single-user, local providers)
compair core up
compair core doctor
compair profile use local

# Single-user installs auto-provision a session; run login once to cache it locally
compair login
compair status

# Inside a repo, create the companion document and track files
compair track

# Same authoring loop
compair group create "Local Demo"
compair group use "Local Demo"
compair review
compair reports
```

Use your own OpenAI key instead of the bundled local providers:
```bash
compair core config set --provider openai --openai-api-key "$OPENAI_API_KEY"
compair core up
```

Switch back to the default free local path:
```bash
compair core config set --provider local
compair core up
```

Shut the container down later:
```bash
compair core logs --tail 200
compair core restart
compair core down
compair core down --purge   # also remove the Docker data volume
```

See `docs/core_quickstart.md` for the self-hosted Core flow and `docs/user_guide.md` for full workflows.
See `docs/ci_review_examples.md` for GitHub Actions and GitLab CI review patterns.

Long review runs emit periodic progress lines while waiting on server processing and new feedback. Remaining-time estimates are approximate.

## Profiles
Profiles let you switch between Cloud and Core endpoints without rebuilding the CLI.

```bash
compair profile ls
compair profile set staging --api-base https://staging.compair.local
compair profile use staging
```

The CLI resolves the API base with the following precedence: `--api-base` flag → `COMPAIR_API_BASE` → selected profile (or `COMPAIR_PROFILE`). Capability data is cached per API base; switching profiles clears that cache automatically.

Snapshots index the full repo by default. If you want a lighter baseline for a specific profile, store explicit limits:
```bash
compair profile set cloud --snapshot-max-files 80 --snapshot-max-total-bytes 500000
```

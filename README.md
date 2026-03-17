# Compair CLI

Compair CLI is built for one problem that single-repo AI review does not solve well: cross-repo drift.

Track your backend, frontend, SDK, CLI, desktop app, and docs as related documents in the same Compair group. Compair indexes each repo, compares new changes against the rest of the group, and surfaces notifications when it sees contract conflicts, hidden overlap, or information gaps.

This is the core difference from GitHub-native AI review. A native review bot sees one pull request in one repository. Compair can tell you that a frontend rename, CLI retry policy, or desktop workflow no longer matches the backend or another client.

## Why teams use it

- Catch API/UI/SDK drift before it reaches users.
- Review one repo in the context of the other repos it depends on.
- Turn high-confidence cross-repo notifications into CI checks that can fail when needed.
- Keep the same workflow locally, in VS Code, and in GitHub Actions.

## Build

```bash
go build -o compair .
```

## Choose Your Start

Use whichever path lowers friction for you:

| If you want to... | Use | Best for |
| --- | --- | --- |
| Try Compair locally without creating an account | `./compair profile use local` + `./compair core up` | Developers evaluating the open/local workflow |
| Use a hosted shared service right away | `./compair profile use cloud` + `./compair login` | Teams who want the simplest shared setup |

My recommendation for the front page is to lead with the open/local path and then present Cloud as the easier hosted option. That gives technically skeptical users a low-trust, low-friction way to try the product first, while still keeping the hosted path obvious for teams that do not want to run anything themselves.

## Quick Start

```bash
# Build the binary
go build -o compair .

# Run the disposable demo
./compair demo

# Try the open/local path first
./compair profile use local
./compair core up
./compair login
./compair whoami

# Or use the hosted service
./compair profile use cloud
./compair login
```

If you want the full local Core flow instead, see [docs/core_quickstart.md](docs/core_quickstart.md).

## Cross-Repo Self-Test

If you want to test the real Compair value proposition on a multi-repo workspace, use this flow.

```bash
# 1. Choose a profile, log in, and create a shared review group
./compair profile use local
# or: ./compair profile use cloud
./compair login
./compair group create "Product Suite"
./compair group use "Product Suite"
./compair self-feedback on
./compair feedback-length brief

# 2. First-run bootstrap only:
# index each related repo before asking for cross-repo feedback
./compair track ~/code/backend-api --initial-sync --no-feedback
./compair track ~/code/web-app --initial-sync --no-feedback
./compair track ~/code/developer-cli --initial-sync --no-feedback
./compair track ~/code/desktop-client --initial-sync --no-feedback
# repeat for any other repos in the shared product surface

# 3. Run the warm review pass across the whole group
./compair review --all --snapshot-mode snapshot --reanalyze-existing --feedback-wait 90

# 4. Inspect the results
./compair reports
./compair notifications
```

Recommended defaults for this workflow:

- Use `brief` feedback length for the first full-suite pass.
- Expect the first baseline of larger repos to take the longest.
- After the group is warmed up, use normal incremental `review` / `sync` runs for day-to-day development.
- `--initial-sync --no-feedback` is only the one-time bootstrap step that tells Compair to index first and compare second.

For the full step-by-step workflow, including what to expect and how to simulate CI locally, see [docs/cross_repo_workflow.md](docs/cross_repo_workflow.md).

## Feedback Length

| Setting | Use it when... |
| --- | --- |
| `brief` | You want a fast, readable signal. Recommended for first full-suite reviews and most daily use. |
| `detailed` | You want more context and rationale for a smaller number of findings. |
| `verbose` | You are actively debugging a specific result and want the most supporting detail. |

## CI Checks

Once a repo is tracked in a group with its companion repos, the same CLI can also drive CI checks.

```bash
# Advisory summary
./compair sync --json

# Conservative failing PR check
./compair sync --json --gate api-contract

# Custom threshold
./compair sync --json --fail-on-severity high --fail-on-type potential_conflict
```

If the term "gate" is unfamiliar, treat it as shorthand for "the rule that decides whether CI should fail."

| Command | What it does | Use it when... |
| --- | --- | --- |
| `./compair sync --json` | Advisory only. Produces machine-readable output and a Markdown report, but does not fail CI on its own. | You are introducing Compair and want visibility without disruption. |
| `./compair sync --json --gate api-contract` | Fails CI on high-severity `potential_conflict` notifications. | Best first production preset. |
| `./compair sync --json --gate cross-product` | Fails CI on broader high-severity cross-product issues. | You want more than API contract checks, but still want a conservative threshold. |
| `./compair sync --json --gate review` | Fails CI on high-severity conflicts and review-oriented updates. | You want stronger code-review style enforcement. |
| `./compair sync --json --gate strict` | Fails CI on high and medium issues across a broader set of notification types. | Use on integration or release branches after you trust the signal. |

Recommended rollout:

- Start with advisory mode and keep the Markdown report as an artifact.
- Move to `api-contract` first if you want CI to fail on severe issues.
- Treat medium-severity notifications as review prompts until you trust the signal for that repo set.

See [docs/ci_review_examples.md](docs/ci_review_examples.md) for GitHub Actions and GitLab CI patterns.

## Core And Cloud

### Core (self-hosted)

```bash
./compair core status
./compair core up
./compair profile use local
./compair login
./compair track
./compair review
```

Use your own OpenAI key instead of bundled local providers:

```bash
./compair core config set --provider openai --openai-api-key "$OPENAI_API_KEY"
./compair core up
```

### Cloud (hosted SaaS)

```bash
./compair profile use cloud
./compair login
./compair group ls
./compair track
./compair review
./compair notifications
```

Long review runs emit periodic progress lines while waiting on server processing and newly generated feedback. Remaining-time estimates are approximate.

## Profiles

Profiles let you switch between Cloud and Core endpoints without rebuilding the CLI.

```bash
./compair profile ls
./compair profile set staging --api-base https://staging.compair.local
./compair profile use staging
```

The CLI resolves the API base with the following precedence: `--api-base` flag -> `COMPAIR_API_BASE` -> selected profile (or `COMPAIR_PROFILE`). Capability data is cached per API base; switching profiles clears that cache automatically.

Snapshots index the full repo by default. If you want a lighter baseline for a specific profile, store explicit limits:

```bash
./compair profile set cloud --snapshot-max-files 80 --snapshot-max-total-bytes 500000
```

## Documentation Map

Start here:

- [Cross-Repo Workflow](docs/cross_repo_workflow.md)
- [User Guide](docs/user_guide.md)
- [CI Review Examples](docs/ci_review_examples.md)

Additional docs:

- [Core Quickstart](docs/core_quickstart.md)
- [Hook Recipes](docs/hook_recipes.md)
- [API Mapping](docs/api_mapping.md)
- [Deployment Guide](docs/deployment_guide.md)
- [Operator Guide](docs/operator_guide.md)
- [CI & Release](docs/ci_release.md)
- [Release Checklist](docs/release_checklist.md)
- [Release Notes Template](docs/release_notes_template.md)

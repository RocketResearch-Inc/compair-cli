# Compair CLI

[![CI](https://img.shields.io/github/actions/workflow/status/RocketResearch-Inc/compair-cli/ci.yml?branch=main&label=CI)](https://github.com/RocketResearch-Inc/compair-cli/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/RocketResearch-Inc/compair-cli)](https://github.com/RocketResearch-Inc/compair-cli/releases)
[![License](https://img.shields.io/github/license/RocketResearch-Inc/compair-cli)](LICENSE)

**Compair CLI helps developers catch cross-repo drift from the terminal.**
Track your backend, frontend, SDK, CLI, desktop app, and docs in one shared review context. Compair compares changes across related repos and surfaces conflicts, hidden overlap, and missing updates before they turn into broken workflows or user-facing issues.

**Compair is a context manager for teams.**
Instead of asking one model call to hold your whole product in working memory, Compair keeps a shared, persistent cross-repo context for the team, narrows attention to the changed surface, and brings in the few related snippets that actually matter.

**Why it's different:** most AI review tools look at one pull request in one repo. Compair reviews a repo in the context of the other repos it depends on.

- Catch backend/frontend/SDK/docs drift earlier
- Review changes in the context of the rest of your product
- Turn high-confidence findings into CI checks when you're ready

**Positioning note:** Compair Cloud is the strongest out-of-the-box experience today. It gives you the best review quality without bringing your own model key, plus hosted auth, shared accounts, email delivery, and the most polished team workflow. Local Core remains the right fit for self-hosting, evaluation, and offline/local setups, with two meaningful bring-your-own-key paths: keep embeddings local and use OpenAI for generation as the lower-outsourced-cost default, or use OpenAI for both generation and embeddings when you want the strongest current self-hosted quality.

## Why This Isn't Just RAG

Traditional RAG is good at answering questions from retrieved snippets. Repo-scoped AI review is good at helping inside one repo or one pull request.

Compair is built for a different problem:

- start from what changed, not from a free-form query
- search across the other repos that make up the product surface
- look for contradictions, drift, hidden overlap, and missing downstream updates
- turn high-confidence findings into notifications and CI gates

This matters because larger context windows do not mean every token is equally weighted, inspected, or analyzed. Important evidence still gets lost when it is buried inside a huge prompt, especially once instructions, history, and output budget are sharing that same window. Compair improves signal by focusing attention on the changed chunk and the most relevant cross-repo evidence instead of asking the model to reason over the whole product at once.

The practical takeaway is simple: Compair wins less by stuffing everything into one prompt and more by repeatedly compressing a large shared code and document surface into a small grounded evidence pack around each change.

## Care to Compair? Try It In 5 Minutes

The fastest way to see what Compair does:

```bash
# 1) Install Compair CLI
# 2) Run the disposable demo
compair demo
```

**What the demo does**

- creates a disposable workspace
- tracks two small related repos
- runs a real Compair review
- shows the kind of cross-repo issues Compair is built to catch

**Start here if:** you want the fastest possible first pass before trying Compair on your own repos.

## Install

Choose the path that fits your workflow:

| Platform | Recommended path | Status |
| -------- | ---------------- | ------ |
| macOS | Homebrew cask | Live |
| Linux (Debian/Ubuntu) | APT repo or GitHub Release | Live |
| Linux (Fedora/RHEL) | RPM repo or GitHub Release | Live |
| Windows | GitHub Release zip | Live |
| Windows | WinGet | Pending upstream approval |
| Any | Build from source | Live |

### Homebrew cask (macOS)

```bash
brew tap RocketResearch-Inc/tap
brew install --cask compair
```

### Linux package repos

Debian / Ubuntu:

```bash
curl -fsSL https://rocketresearch-inc.github.io/compair-packages/install/debian.sh | bash
```

Fedora / RHEL:

```bash
# Omit sudo if you are already root (for example, inside a container).
curl -fsSL https://rocketresearch-inc.github.io/compair-packages/install/compair.repo | sudo tee /etc/yum.repos.d/compair.repo >/dev/null
sudo dnf install -y compair
```

### Download a release

Start from the [GitHub Releases](https://github.com/RocketResearch-Inc/compair-cli/releases) page. Release archives are published for macOS, Linux, and Windows.

Windows example:

```powershell
# Download the latest Windows zip from GitHub Releases, unzip it, then:
.\compair.exe version
```

### Build from source

```bash
go build -o compair .
```

If you want source-based install details or deeper command reference material, see [docs/user_guide.md](docs/user_guide.md).

## Choose Your Start

### Demo

Use this if you want to see Compair end-to-end in a disposable workspace.

```bash
compair demo
```

### Local / self-hosted

Use this if you want to evaluate Compair locally with managed Core.

```bash
compair profile use local
compair core up
compair login
```

If you stay fully local with the bundled no-key providers, expect functional but simpler summaries than Cloud. For the best lower-outsourced-cost self-hosted start, keep embeddings local and use your own OpenAI key for generation. If you want the strongest current self-hosted review quality, use your own OpenAI key for both generation and embeddings.

### Cloud

Use this if you want the simplest shared setup.

```bash
compair profile use cloud
compair login
```

Cloud is the best default when you want the strongest first impression, the least setup friction, and the best shared team workflow.

**New here? Start with `compair demo`.**
**Evaluating open/local? Start with Local.**
**Working with teammates right away? Start with Cloud.**

## Example

You change an API field name in a backend repo.
The web app and CLI still reference the old name.

Compair reviews the repos together and flags the mismatch before the change reaches users or turns into a broken workflow.

```text
Potential Conflict
backend-api: review response now uses `items`
web-app / developer-cli: still read `reviews`
Likely impact: clients show fallback values or missing review data
```

Compair surfaced a high-confidence drift issue across related repos that would not appear in a single-repo review.

## Try It On Your Own Repo Suite

Use this after you've run the demo and want to test Compair on the repos that make up your actual product surface.

Before you start:

- Put all related repos in one group
- Upload baselines first
- Then run one warm review across the group

```bash
# 1. Choose a profile and create a shared review group
compair profile use local
# or: compair profile use cloud
compair login
compair group create "Product Suite"
compair group use "Product Suite"
compair self-feedback on
compair feedback-length brief

# 2. First-run bootstrap only:
# index each related repo before asking for cross-repo feedback
compair track ~/code/backend-api --initial-sync --no-feedback
compair track ~/code/web-app --initial-sync --no-feedback
compair track ~/code/developer-cli --initial-sync --no-feedback
compair track ~/code/desktop-client --initial-sync --no-feedback
# repeat for any other repos in the shared product surface

# 3. Run the warm review pass across the whole group
compair review --all --snapshot-mode snapshot --reanalyze-existing --feedback-wait 90

# 4. Inspect the results
compair reports
compair notifications
compair notifications prefs
```

After the first run:

- Start with `brief`
- Expect the first baseline to take longest
- After the warm pass, use normal `review` / `sync` cycles day to day
- Treat `--initial-sync --no-feedback` as a one-time bootstrap step, not the normal daily workflow

For the full step-by-step workflow, see [docs/cross_repo_workflow.md](docs/cross_repo_workflow.md).

## Feedback Length

| Setting    | Use it when...                                                                                 |
| ---------- | ---------------------------------------------------------------------------------------------- |
| `brief`    | You want a fast, readable signal. Recommended for first full-suite reviews and most daily use. |
| `detailed` | You want more context and rationale for a smaller number of findings.                          |
| `verbose`  | You are actively debugging a specific result and want the most supporting detail.              |

## Add Compair To CI When You're Ready

Start in advisory mode:

```bash
compair sync --json
```

Move to a conservative failing check:

```bash
compair sync --json --gate api-contract
```

Tighten rules later as you build trust in the signal.

If the term `gate` is unfamiliar, treat it as the rule that decides whether CI should fail.

| Command                                    | What it does                                                                                            | Use it when...                                                                   |
| ------------------------------------------ | ------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------- |
| `compair sync --json`                      | Advisory only. Produces machine-readable output and a Markdown report, but does not fail CI on its own. | You are introducing Compair and want visibility without disruption.              |
| `compair sync --json --gate api-contract`  | Fails CI on high-severity `potential_conflict` notifications.                                           | Best first production preset.                                                    |
| `compair sync --json --gate cross-product` | Fails CI on broader high-severity cross-product issues.                                                 | You want more than API contract checks, but still want a conservative threshold. |
| `compair sync --json --gate review`        | Fails CI on high-severity conflicts and review-oriented updates.                                        | You want stronger code-review style enforcement.                                 |
| `compair sync --json --gate strict`        | Fails CI on high and medium issues across a broader set of notification types.                          | Use on integration or release branches after you trust the signal.               |

**Recommended rollout:** start with visibility, then fail only on the highest-confidence issues, then tighten thresholds later.

See [docs/ci_review_examples.md](docs/ci_review_examples.md) for GitHub Actions and GitLab CI examples.

## Docs

**New users should start with the demo, user guide, or cross-repo workflow.**
**Maintainers and operators can use the advanced docs below.**

### Start Here

- [Try it in 5 minutes](#try-it-in-5-minutes)
- [User Guide](docs/user_guide.md)
- [Cross-Repo Workflow](docs/cross_repo_workflow.md)
- [Core Quickstart](docs/core_quickstart.md)
- [CI Review Examples](docs/ci_review_examples.md)

### Advanced / Maintainer Docs

- [Deployment Guide](docs/deployment_guide.md)
- [Operator Guide](docs/operator_guide.md)
- [Launch Validation](docs/launch_validation.md)
- [How We Evaluated Quality](docs/quality_evaluation.md)
- [CI & Release](docs/ci_release.md)
- [Release Checklist](docs/release_checklist.md)
- [Release Notes Template](docs/release_notes_template.md)
- [API Mapping](docs/api_mapping.md)
- [Hook Recipes](docs/hook_recipes.md)
- [Config Reference](docs/config_reference.md)

### **What will you Compair?**

For any issues create one here or reach out to steven@compair.sh

# Compair CLI

[![CI](https://img.shields.io/github/actions/workflow/status/RocketResearch-Inc/compair-cli/ci.yml?branch=main&label=CI)](https://github.com/RocketResearch-Inc/compair-cli/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/RocketResearch-Inc/compair-cli)](https://github.com/RocketResearch-Inc/compair-cli/releases)
[![License](https://img.shields.io/github/license/RocketResearch-Inc/compair-cli)](LICENSE)

**Compair is a semantic CI check for docs/code drift.**
Tests tell you whether code still runs. Docs builds tell you whether pages still render. Compair checks whether docs, code, examples, configs, SDKs, CLIs, and related repos still agree.

Under the hood, Compair acts like a shared context manager for your product surface. Instead of asking one model call to hold your whole product in working memory, Compair keeps persistent cross-repo context, narrows attention to the changed surface, and brings in the few related snippets that actually matter.

**Why it's different:** most AI review tools look at one pull request in one repo. Compair reviews a repo in the context of the other repos it depends on.

- Catch docs/code/API/SDK/config drift earlier
- Review changes in the context of the rest of your product
- Turn high-confidence findings into advisory or failing CI checks when you're ready

## Install

Choose the path that fits your platform.

| Platform | Recommended install | Notes |
| -------- | ------------------- | ----- |
| macOS | `brew tap RocketResearch-Inc/tap`<br>`brew install --cask compair` | Fastest macOS path. |
| Debian / Ubuntu | `curl -fsSL https://rocketresearch-inc.github.io/compair-packages/install/debian.sh \| bash` | Installs from the Compair APT repo. |
| Fedora / RHEL | `curl -fsSL https://rocketresearch-inc.github.io/compair-packages/install/compair.repo \| sudo tee /etc/yum.repos.d/compair.repo >/dev/null`<br>`sudo dnf install -y compair` | Omit `sudo` if you are already root. |
| Windows | Download the latest zip from [GitHub Releases](https://github.com/RocketResearch-Inc/compair-cli/releases), unzip it, then run `.\compair.exe version`. | WinGet is pending upstream approval. |
| Any | `go build -o compair .` | Best for contributors and local hacking. |

Release archives are published for macOS, Linux, and Windows on the [GitHub Releases](https://github.com/RocketResearch-Inc/compair-cli/releases) page. If you want deeper install details or command reference material, see [docs/user_guide.md](docs/user_guide.md).

## Try It In 5 Minutes

After installing, run the offline demo and skim the report it writes:

```bash
compair demo --offline
```

No production repo setup, Cloud account, Docker, or model key required.

**What the offline demo does**

- creates a disposable workspace
- seeds two small related repos with an intentional API/client mismatch
- renders a prebaked Compair report
- leaves your real repos untouched

Want to preview the report before installing? Open the demo-generated [sample output](docs/sample_output.md).

When you want fresh generated feedback instead of the prebaked sample, use `compair demo --mode local` or `compair demo --mode cloud`.

## Choose Your Start

Use the offline demo for the first look. When you are ready for fresh feedback, choose one of these live paths.

### Local / self-hosted

Use this if you want to evaluate Compair locally with managed Core.

```bash
compair profile use local
compair core up
compair login
```

If you stay fully local with the bundled no-key providers, expect functional but simpler summaries than Cloud. For the best lower-outsourced-cost self-hosted start, keep embeddings local and use your own OpenAI key for generation:

```bash
export OPENAI_API_KEY="sk-..."
compair core config set --generation-provider openai --embedding-provider local --openai-model gpt-5.4-mini --openai-api-key "$OPENAI_API_KEY"
compair core restart
```

If you do not want the key saved in `~/.compair/core_runtime.yaml`, set `COMPAIR_OPENAI_API_KEY` or `OPENAI_API_KEY` in your shell and omit `--openai-api-key`.

### Cloud

Use this if you want the simplest shared setup.

```bash
compair profile use cloud
compair signup --email you@example.com --name "Your Name"
compair login
```

Skip `compair signup` if you already have an account. Cloud is the best default when you want the strongest first impression, the least setup friction, and the best shared team workflow.

If you are unsure, use Local for open or self-hosted evaluation and Cloud for the simplest shared team setup.

## Help Test Compair

Compair CLI is ready for early developer testing.

Try the offline demo first. If you want a live review, rerun it with `--mode local` or `--mode cloud`.

Feedback is especially useful from developers maintaining backend + frontend repos, API + SDK repos, CLI + cloud service repos, docs + implementation repos, or multi-repo internal tools.

Please open an issue with what worked, what broke, and where the output was confusing. Include your OS, install path, and whether you tested offline, local Core, or Cloud when you can.

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

# Optional but recommended for larger suites:
# keep generated artifacts and low-signal files out of the review surface
# with repo-local .compairignore files before the first warm pass
compair ignore suggest ~/code/backend-api
# add --write to append high-confidence suggestions after review

# 3. Run the warm review pass across the whole group
compair review --all --snapshot-mode snapshot --reanalyze-existing --detach
compair wait --all

# Optional: if you want a slower, broader repo-pair sweep instead of the
# standard shared-peer review pool, run the attached pairwise mode
compair review --all --pairwise --cross-repo-only

# Optional: if you want a one-shot whole-bundle read instead of the normal
# per-chunk retrieval/index path, run review --now
compair review --all --snapshot-mode snapshot --reanalyze-existing --now --yes
compair review --all --snapshot-mode snapshot --reanalyze-existing --now --skip-index --yes

# 4. Inspect the results
compair reports
compair notifications
compair notifications prefs
```

After the first run:

- Start with `brief`
- Expect the first baseline to take longest
- After the warm pass, use normal `review` / `wait` cycles day to day
- Use `review --detach` when you want the same workflow without blocking your terminal
- Use `wait --timeout 20m` when a large baseline needs more time without resubmitting
- Use `review --pairwise` when you want a slower, higher-coverage repo-pair pass; `--cross-repo-only` skips same-repo pairs
- Use `review --now` when you want one whole-bundle LLM pass over the current tracked repo set instead of the normal per-chunk retrieval path; the CLI prints a token/cost quote before the model call, and Cloud runs require prepaid credits once that feature is enabled
- Use `review --now --skip-index` when you want that bundle review faster and can tolerate the indexed retrieval state staying stale until a later full sync/review
- Use `ignore suggest` to find repo-local `.compairignore` candidates before a full-suite baseline
- Treat `sync` as the advanced/CI control surface rather than the default daily command
- Treat `--initial-sync --no-feedback` as a one-time bootstrap step, not the normal daily workflow

For the full step-by-step workflow, see [docs/cross_repo_workflow.md](docs/cross_repo_workflow.md).

## Feedback Length

| Setting    | Use it when...                                                                                 |
| ---------- | ---------------------------------------------------------------------------------------------- |
| `brief`    | You want a fast, readable signal. Recommended for first full-suite reviews and most daily use. |
| `detailed` | You want more context and rationale for a smaller number of findings.                          |
| `verbose`  | You are actively debugging a specific result and want the most supporting detail.              |

## Add Compair To CI When You're Ready

For interactive use, prefer `compair review`, `compair review --detach`, and `compair wait`.
Use `compair sync` when you specifically want CI, machine-readable output, gating, or lower-level control.

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

## Why This Isn't Just RAG

Traditional RAG is good at answering questions from retrieved snippets. Repo-scoped AI review is good at helping inside one repo or one pull request.

Compair is built for a different problem:

- start from what changed, not from a free-form query
- search across the other repos that make up the product surface
- look for contradictions, drift, hidden overlap, and missing downstream updates
- turn high-confidence findings into notifications and CI gates

This matters because larger context windows do not mean every token is equally weighted, inspected, or analyzed. Important evidence still gets lost when it is buried inside a huge prompt, especially once instructions, history, and output budget are sharing that same window. Compair improves signal by focusing attention on the changed chunk and the most relevant cross-repo evidence instead of asking the model to reason over the whole product at once.

The practical takeaway is simple: Compair wins less by stuffing everything into one prompt and more by repeatedly compressing a large shared code and document surface into a small grounded evidence pack around each change.

## Docs

**New users should start with the demo, user guide, or cross-repo workflow.**
**Maintainers and operators can use the advanced docs below.**

### Start Here

- [Try it in 5 minutes](#try-it-in-5-minutes)
- [Sample Output](docs/sample_output.md)
- [User Guide](docs/user_guide.md)
- [Cross-Repo Workflow](docs/cross_repo_workflow.md)
- [Core Quickstart](docs/core_quickstart.md)
- [CI Review Examples](docs/ci_review_examples.md)

### Advanced / Operator Docs

- [Deployment Guide](docs/deployment_guide.md)
- [Operator Guide](docs/operator_guide.md)
- [API Mapping](docs/api_mapping.md)
- [Hook Recipes](docs/hook_recipes.md)
- [Config Reference](docs/config_reference.md)

Internal launch, validation, release, and packaging runbooks are maintained
outside the public docs set.

### **What will you Compair?**

For any issues create one here or reach out to steven@compair.sh

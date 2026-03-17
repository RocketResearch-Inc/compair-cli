# Cross-Repo Workflow

This is the recommended Compair CLI workflow when your product spans multiple repos and you want Compair to review them as one connected system.

Typical examples:

- backend + frontend
- backend + CLI
- API + desktop app
- service + docs or policy repo

Compair is strongest when those repos live in the same group. That gives each repo a peer set to compare against instead of reviewing a repo in isolation.

## What To Read First

- Read this page for the recommended workflow.
- Use [user_guide.md](user_guide.md) as the command reference.
- Use [ci_review_examples.md](ci_review_examples.md) when you are ready to move the same pattern into GitHub Actions or GitLab CI.

## Recommended Local Self-Test

Use this the first time you want to prove out Compair on a repo suite.

### 1. Choose a profile and create a shared group

```bash
compair profile use local
# or: compair profile use cloud
compair login
compair group create "Product Suite"
compair group use "Product Suite"
compair self-feedback on
compair feedback-length brief
```

Why:

- all related repos need to live in the same group
- `self-feedback on` lets your own repos act as peer references
- `brief` keeps the first full-suite run readable

### 2. Baseline each repo without feedback

```bash
compair track ~/code/backend-api --initial-sync --no-feedback
compair track ~/code/web-app --initial-sync --no-feedback
compair track ~/code/developer-cli --initial-sync --no-feedback
compair track ~/code/desktop-client --initial-sync --no-feedback
# repeat for any other repos that make up the same product surface
```

Why:

- this registers each repo as its own Compair document
- the initial sync indexes the baseline snapshot
- `--no-feedback` avoids generating feedback while the group is still incomplete
- this is only the first-run bootstrap; you should not need these flags for normal daily review

### 3. Run the warm pass

```bash
compair review --all --snapshot-mode snapshot --reanalyze-existing --feedback-wait 90
```

Why:

- `--all` reviews every tracked repo in the active group
- `--snapshot-mode snapshot` uploads full repo snapshots for the baseline-style pass
- `--reanalyze-existing` tells Compair to generate feedback from already-indexed chunks when there are no new chunks

### 4. Inspect the results

```bash
compair reports
compair notifications
```

Check:

- whether the report surfaces a real cross-repo issue
- whether the issue is understandable without reading raw payloads
- whether the severity feels appropriate
- whether the notification should be advisory or should fail CI

## What To Expect On The First Run

- Large repos can take noticeably longer on the first baseline because Compair is indexing and embedding many chunks.
- The first full-suite pass is the noisiest report you will see. Use `brief` first.
- Once the repos are warmed up, day-to-day runs should be incremental and much faster than the initial suite baseline.

## Day-To-Day Workflow After The Baseline

For normal development, do not repeat the full baseline process.

Use:

```bash
compair review
compair sync --json
compair notifications
```

That keeps the common loop lightweight:

1. make a change
2. run `compair review` in the repo you changed
3. inspect the report or notification
4. fix or intentionally accept the drift

When you want a broader integration check across the suite again, rerun:

```bash
compair review --all
```

## Local CI Simulation

Before wiring GitHub Actions, test the gate locally from a tracked repo.

```bash
compair sync --json --gate api-contract
```

That is the recommended first "fail CI" policy because it is conservative: fail only when Compair sees high-severity contract conflict signals.

Useful variants:

```bash
compair sync --gate help
compair sync --json --fail-on-severity high --fail-on-type potential_conflict
compair sync --json --fail-on-severity high,medium --fail-on-type potential_conflict
```

## Suggested Rollout

Use this maturity ladder:

1. Local advisory review only.
2. GitHub Action advisory review with report artifacts.
3. CI failures for `high` `potential_conflict`.
4. Stricter branch or release gates only after you trust the signal.

This keeps Compair from feeling like noisy lint while still giving it room to become a serious integration gate.

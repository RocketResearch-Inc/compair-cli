# CI Review Examples

These examples assume the repository checkout path is `compair-cli/`. If your checkout path differs, update the `working-directory` and binary paths accordingly.

These examples show how to run Compair in CI once a repo has already been tracked and bound to a Compair document.

Compair is most valuable in CI when that document belongs to a group that also contains the other repos it depends on. The CI job still runs in one checkout, but the review compares that repo against peer repos already indexed in the same Compair group.

## Recommended Setup

Before using Compair in CI:

1. Create a dedicated Compair user for CI.
2. Log in once and store the resulting token in CI secret storage as `COMPAIR_AUTH_TOKEN`.
3. Track the repo once from a trusted workstation:
```bash
compair login
compair group use <group-id>
compair track
```
4. Either:
   - commit `.compair/config.yaml`, or
   - base64-encode it into a CI secret and restore it during the workflow.

Recommended secret-backed pattern:

```bash
base64 < .compair/config.yaml
```

Store that value as `COMPAIR_PROJECT_CONFIG_YAML_B64`, then restore it inside CI
and point the CLI at it with `COMPAIR_PROJECT_CONFIG_PATH`.

Without a stable project config, CI will not know which Compair document the
repo should continue updating.

## Cross-Repo Setup

For cross-repo checks, do this once from a trusted workstation before enabling CI:

1. Create a shared Compair group for the related repos.
2. Track each repo into that group.
3. Baseline the group with `--initial-sync --no-feedback`.
4. Run a warm review pass so the group already has peer context before the first CI run.

See [cross_repo_workflow.md](cross_repo_workflow.md) for the full local setup flow.

## GitHub Actions: Advisory Review

This uploads the current repo, compares it against its peer repos in the same Compair group, writes the default Markdown report, and stores it as a build artifact without blocking the PR.

```yaml
name: compair-review

on:
  pull_request:
  workflow_dispatch:

jobs:
  review:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - uses: actions/setup-go@v5
        with:
          go-version: "1.24.x"

      - name: Build Compair CLI
        working-directory: compair-cli
        run: go build -o compair .

      - name: Restore Compair binding
        run: |
          mkdir -p "$RUNNER_TEMP/compair-state"
          echo "${{ secrets.COMPAIR_PROJECT_CONFIG_YAML_B64 }}" | base64 --decode > "$RUNNER_TEMP/compair-state/project-config.yaml"

      - name: Login to Compair
        working-directory: compair-cli
        run: ./compair --api-base https://app.compair.sh/api login --token "${{ secrets.COMPAIR_AUTH_TOKEN }}"

      - name: Run advisory review
        working-directory: ${{ github.workspace }}
        env:
          COMPAIR_PROJECT_CONFIG_PATH: ${{ runner.temp }}/compair-state/project-config.yaml
        run: ./compair-cli/compair --api-base https://app.compair.sh/api review || true

      - name: Upload report artifact
        uses: actions/upload-artifact@v4
        with:
          name: compair-feedback
          path: .compair/latest_feedback_sync.md
          if-no-files-found: ignore
```

## GitHub Actions: Failing PR Check

This keeps the artifact, but fails the job when the configured Compair rule matches severe notifications.

```yaml
name: compair-gate

on:
  pull_request:

jobs:
  gate:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - uses: actions/setup-go@v5
        with:
          go-version: "1.24.x"

      - name: Build Compair CLI
        working-directory: compair-cli
        run: go build -o compair .

      - name: Restore Compair binding
        run: |
          mkdir -p "$RUNNER_TEMP/compair-state"
          echo "${{ secrets.COMPAIR_PROJECT_CONFIG_YAML_B64 }}" | base64 --decode > "$RUNNER_TEMP/compair-state/project-config.yaml"

      - name: Login to Compair
        working-directory: compair-cli
        run: ./compair --api-base https://app.compair.sh/api login --token "${{ secrets.COMPAIR_AUTH_TOKEN }}"

      - name: Run failing PR check
        working-directory: ${{ github.workspace }}
        env:
          COMPAIR_PROJECT_CONFIG_PATH: ${{ runner.temp }}/compair-state/project-config.yaml
        run: ./compair-cli/compair --api-base https://app.compair.sh/api sync --json --gate api-contract

      - name: Upload report artifact
        if: always()
        uses: actions/upload-artifact@v4
        with:
          name: compair-feedback
          path: .compair/latest_feedback_sync.md
          if-no-files-found: ignore
```

## GitLab CI: Advisory Review

```yaml
stages:
  - review

compair_review:
  stage: review
  image: golang:1.24
  script:
    - cd compair-cli
    - go build -o compair .
    - mkdir -p "$CI_PROJECT_DIR/.compair-ci"
    - echo "$COMPAIR_PROJECT_CONFIG_YAML_B64" | base64 --decode > "$CI_PROJECT_DIR/.compair-ci/project-config.yaml"
    - ./compair --api-base https://app.compair.sh/api login --token "$COMPAIR_AUTH_TOKEN"
    - cd "$CI_PROJECT_DIR"
    - COMPAIR_PROJECT_CONFIG_PATH="$CI_PROJECT_DIR/.compair-ci/project-config.yaml" ./compair-cli/compair --api-base https://app.compair.sh/api review || true
  artifacts:
    when: always
    paths:
      - .compair/latest_feedback_sync.md
```

## GitLab CI: Failing Check

```yaml
stages:
  - review

compair_gate:
  stage: review
  image: golang:1.24
  script:
    - cd compair-cli
    - go build -o compair .
    - mkdir -p "$CI_PROJECT_DIR/.compair-ci"
    - echo "$COMPAIR_PROJECT_CONFIG_YAML_B64" | base64 --decode > "$CI_PROJECT_DIR/.compair-ci/project-config.yaml"
    - ./compair --api-base https://app.compair.sh/api login --token "$COMPAIR_AUTH_TOKEN"
    - cd "$CI_PROJECT_DIR"
    - COMPAIR_PROJECT_CONFIG_PATH="$CI_PROJECT_DIR/.compair-ci/project-config.yaml" ./compair-cli/compair --api-base https://app.compair.sh/api sync --json --gate api-contract
  artifacts:
    when: always
    paths:
      - .compair/latest_feedback_sync.md
```

## Practical Guidance

- Start with advisory mode.
- Only promote to failing checks once you trust the signal quality for your repos.
- Use a dedicated Compair account and group for CI.
- If you do not want to commit `.compair/config.yaml`, restore it from CI secret storage and set `COMPAIR_PROJECT_CONFIG_PATH`.
- Prefer `api-contract` as the first failing-check preset. It is the most conservative option.
- Use advisory mode for medium-severity findings until you trust the signal for that repo set.
- Keep the Markdown artifact even on failure so reviewers can see why the job failed.

## Optional: Post a PR Comment

Compair itself does not post PR comments directly today. That step depends on the hosting platform token and permissions.

For GitHub, a common pattern is:

```yaml
      - name: Comment on PR
        if: always() && github.event_name == 'pull_request'
        env:
          GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}
          PR_NUMBER: ${{ github.event.pull_request.number }}
        run: |
          if [ -f .compair/latest_feedback_sync.md ]; then
            gh pr comment "$PR_NUMBER" --body-file .compair/latest_feedback_sync.md
          fi
```

For GitLab, use the project access token or job token with the Merge Request notes API.

This is intentionally left as CI/platform configuration rather than a built-in CLI behavior, because the correct permissions and comment target vary by host.

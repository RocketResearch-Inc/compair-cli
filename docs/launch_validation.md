# Launch Validation

Use this checklist before broadly promoting Compair CLI. It is written against the current Compair workspace layout:

```bash
export COMPAIR_ROOT=~/Code/compair
```

Adjust `COMPAIR_ROOT` if your checkout lives elsewhere.

## 1. Install-path validation

### macOS: Homebrew + Cloud demo

```bash
brew untap RocketResearch-Inc/tap 2>/dev/null || true
brew tap RocketResearch-Inc/tap
brew reinstall --cask compair
compair version
compair demo --mode cloud
```

### Linux: package repo + Local Core demo

```bash
curl -fsSL https://rocketresearch-inc.github.io/compair-packages/install/debian.sh | bash
compair version
compair demo --mode local
```

### Windows: GitHub Release zip + Cloud demo

```powershell
# Download the latest Windows zip from GitHub Releases, unzip it, then:
.\compair.exe version
.\compair.exe demo --mode cloud
```

Success criteria:

- `compair version` runs on each platform
- the demo completes and writes a Markdown report
- the report contains one real cross-repo notification instead of fallback or duplicate noise

## 2. First-run matrix

Use a clean home directory for each scenario so cached credentials and profiles do not hide first-run issues.

### Cloud demo with fresh config

```bash
export HOME="$(mktemp -d)"
compair demo --mode cloud
```

### Local Core demo with Docker and no OpenAI key

```bash
export HOME="$(mktemp -d)"
compair demo --mode local
```

Treat this as a functionality and UX check, not the primary proof point for review quality. The bundled no-key local path should work and stay readable, but Cloud or OpenAI-backed local Core is still the stronger demo-quality experience.

### Local Core demo with OpenAI generation and local embeddings

```bash
export HOME="$(mktemp -d)"
compair core config set --generation-provider openai --embedding-provider local --openai-model gpt-5.4-mini --openai-api-key "$OPENAI_API_KEY"
compair core up
compair demo --mode local
```

### Local Core demo with fully OpenAI-backed providers

```bash
export HOME="$(mktemp -d)"
compair core config set --provider openai --openai-model gpt-5.4 --openai-api-key "$OPENAI_API_KEY"
compair core up
compair demo --mode local
```

Success criteria:

- Cloud asks for the expected login flow and then completes
- Local Core stays in single-user mode unless you explicitly switch auth
- The no-key local demo is functional, but any launch screenshots or external examples should come from Cloud or OpenAI-backed local Core instead
- The OpenAI-backed local demo produces natural feedback text instead of fallback reference dumps

### Current macOS local-Core validation snapshot

These results reflect the current seeded-drift suite on macOS using Local Core. Use them to choose the recommended launch path and to set expectations in docs or demos.

| Local Core configuration | Scenario A | Scenario B | Scenario C | Scenario D | Current read |
| --- | --- | --- | --- | --- | --- |
| `embedding=openai`, `generation=openai` | pass | pass | pass | strict-pass, softer than ideal | Best local-Core quality path tested so far |
| `embedding=local`, `generation=openai` | pass | pass | pass | unstable / soft | Credible lower-outsourced-cost bring-your-own-key path |
| `embedding=local`, `generation=local` | miss | miss | miss | miss | Functional fallback only; not a strong review-quality proof point |

### Current production-Cloud validation snapshot

These results reflect the seeded production-Cloud reruns completed on May 1, 2026 using a fresh CLI `HOME`, an explicit Cloud profile, a fresh validation group per scenario, and a baseline-clear pass before seeding the drift.

| Production Cloud configuration | Scenario A | Scenario B | Scenario C | Scenario D | Current read |
| --- | --- | --- | --- | --- | --- |
| `api_base=https://app.compair.sh/api`, fresh `HOME`, fresh group per scenario | pass | pass | pass, artifact bundle not currently archived under `benchmark_artifacts/launch_validation` | miss / inconclusive | Strongest validated launch path; A, B, and C are the primary proof points, while D remains a softer sanity check |

Artifact bundles:

- Scenario A: `benchmark_artifacts/launch_validation/scenario-a-20260430-233220/`
- Scenario B: `benchmark_artifacts/launch_validation/scenario-b-20260430-224548/`
- Scenario D: `benchmark_artifacts/launch_validation/scenario-d-20260501-000228/`
- Scenario C: validated during the production rerun, but the final artifact bundle still needs to be archived if we want a complete four-scenario paper trail under `benchmark_artifacts/launch_validation/`. Treat this as a documentation-completeness gap, not as a failed validation result.

Operational notes from the production rerun:

- Use the same shell session for `compair_seed_setup` and the later `sync` command. Mixing `HOME`, wrappers, or profile state can resume the wrong pending task and make a seeded scenario look like a product miss.
- Before seeding a drift, run `"$COMPAIR_BIN" sync --all --fetch-only --feedback-wait 0 --process-timeout-sec 0` and confirm that no `pending_task` entries remain in the scenario worktrees.
- Keep the production launch read anchored on Scenarios A, B, and C. Scenario D is still useful for broad install-surface sanity checking, but it is more retrieval-sensitive than the other seeded tests and should not carry equal benchmark weight.

Recommended launch framing:

- Cloud remains the strongest out-of-the-box experience.
- Local Core with OpenAI-backed generation remains the recommended self-hosted quality path.
- Local Core with local embeddings plus OpenAI generation is the lower-outsourced-cost self-hosted path and now has a completed strict replay baseline from the saved `core-local` retrieval run.
- Fully local no-key mode is launchable as a privacy-first, zero-cost, functional fallback and retrieval-assist path, but it should be described as lower-fidelity and should not be the source of launch screenshots, benchmark claims, or primary review-quality examples.

## 3. Real cross-repo suite review

This is the highest-value validation outside the demo swimlane.

```bash
export COMPAIR_ROOT=~/Code/compair

compair profile use local
# or: compair profile use cloud
compair login

compair group create "Compair Launch Validation"
compair group use "Compair Launch Validation"
compair self-feedback on
compair feedback-length brief

compair track "$COMPAIR_ROOT/compair_core" --initial-sync --no-feedback
compair track "$COMPAIR_ROOT/compair_cloud" --initial-sync --no-feedback
compair track "$COMPAIR_ROOT/compair-cli" --initial-sync --no-feedback
compair track "$COMPAIR_ROOT/compair_desktop" --initial-sync --no-feedback
compair track "$COMPAIR_ROOT/compair_site" --initial-sync --no-feedback
compair track "$COMPAIR_ROOT/compair-ui" --initial-sync --no-feedback

compair review --all --snapshot-mode snapshot --reanalyze-existing --feedback-wait 90
compair reports

compair notifications
```

Success criteria:

- the first warm pass completes without hanging
- the report is readable even if it produces zero findings on a clean baseline
- if no feedback is generated here, continue to Step 4 and use the seeded drift scenario below
- `compair notifications` adds useful ranking/rationale instead of duplicating the report text on both Cloud and Core
- if you are preparing launch screenshots or examples, prefer Cloud or OpenAI-backed local runs over the bundled no-key local fallback

## 4. Local CI simulation

Run one seeded drift at a time. Restore it before moving to the next scenario so each gate is tested against one clear change.

No commit is required for these validations. The commands below use `--snapshot-mode snapshot` so Compair reviews the current working tree, not just `LastSyncedCommit..HEAD`.

Important:

- One seeded drift usually produces one new feedback / notification cycle.
- Re-running different gate commands against the exact same unchanged drift will often show zero new feedback, because Compair avoids regenerating feedback for chunks that already have feedback, and the notification gate only evaluates fresh events from the current run.
- If you want to compare multiple gates, reseed or vary the drift between runs.
- A non-zero exit is expected when a blocking gate correctly matches the seeded drift. Treat that as a pass for the gate, then restore the file and move on.
- Use a fresh validation group and fresh worktrees for each seeded scenario. This avoids stale feedback suppression, unrelated local edits in snapshot mode, and duplicate repo documents from partial reruns.
- When switching between Cloud and Local Core for comparison, start the seeded scenario over in a new group instead of reusing the previous run.

### Recommended isolation recipe

Build and use the current CLI binary for seeded validation:

```bash
cd "$COMPAIR_ROOT/compair-cli"
go build -o compair .
export COMPAIR_BIN="$COMPAIR_ROOT/compair-cli/compair"
"$COMPAIR_BIN" version
```

Reusable shell helpers:

```bash
compair_seed_setup() {
  export SCENARIO_KEY="$1"
  export SCENARIO_ROOT="/tmp/compair-${SCENARIO_KEY}"
  export GROUP_NAME="Compair ${SCENARIO_KEY} $(date +%Y%m%d-%H%M%S)"

  rm -rf "$SCENARIO_ROOT"
  mkdir -p "$SCENARIO_ROOT"

  git -C "$COMPAIR_ROOT/compair_core" worktree add "$SCENARIO_ROOT/compair_core" HEAD
  git -C "$COMPAIR_ROOT/compair_cloud" worktree add "$SCENARIO_ROOT/compair_cloud" HEAD
  git -C "$COMPAIR_ROOT/compair-cli" worktree add "$SCENARIO_ROOT/compair-cli" HEAD
  git -C "$COMPAIR_ROOT/compair_desktop" worktree add "$SCENARIO_ROOT/compair_desktop" HEAD
  git -C "$COMPAIR_ROOT/compair_site" worktree add "$SCENARIO_ROOT/compair_site" HEAD

  "$COMPAIR_BIN" group create "$GROUP_NAME"
  "$COMPAIR_BIN" group use "$GROUP_NAME"

  "$COMPAIR_BIN" track "$SCENARIO_ROOT/compair_core" --initial-sync --no-feedback
  "$COMPAIR_BIN" track "$SCENARIO_ROOT/compair_cloud" --initial-sync --no-feedback
  "$COMPAIR_BIN" track "$SCENARIO_ROOT/compair-cli" --initial-sync --no-feedback
  "$COMPAIR_BIN" track "$SCENARIO_ROOT/compair_desktop" --initial-sync --no-feedback
  "$COMPAIR_BIN" track "$SCENARIO_ROOT/compair_site" --initial-sync --no-feedback
}

compair_seed_cleanup() {
  git -C "$COMPAIR_ROOT/compair_core" worktree remove --force "$SCENARIO_ROOT/compair_core"
  git -C "$COMPAIR_ROOT/compair_cloud" worktree remove --force "$SCENARIO_ROOT/compair_cloud"
  git -C "$COMPAIR_ROOT/compair-cli" worktree remove --force "$SCENARIO_ROOT/compair-cli"
  git -C "$COMPAIR_ROOT/compair_desktop" worktree remove --force "$SCENARIO_ROOT/compair_desktop"
  git -C "$COMPAIR_ROOT/compair_site" worktree remove --force "$SCENARIO_ROOT/compair_site"
  rm -rf "$SCENARIO_ROOT"
  unset SCENARIO_KEY SCENARIO_ROOT GROUP_NAME
}
```

Each scenario below assumes you start by running `compair_seed_setup <scenario-name>` and end by running `compair_seed_cleanup`.

### Scenario A: `api-contract`

Seed a temporary capabilities schema drift in `compair-cli`.

This change intentionally makes the CLI expect camelCase capability fields, while Core and Cloud still publish snake_case fields from `/capabilities`.

```bash
compair_seed_setup scenario-a
cd "$SCENARIO_ROOT/compair-cli"

python3 - <<'PY'
from pathlib import Path

path = Path("internal/api/capabilities.go")
text = path.read_text()
text = text.replace('json:"single_user"', 'json:"singleUser"', 1)
text = text.replace('json:"activity_feed"', 'json:"activityFeed"', 1)
path.write_text(text)
print(f"Seeded temporary drift in {path}")
PY

git diff -- internal/api/capabilities.go
"$COMPAIR_BIN" sync --json --gate api-contract --snapshot-mode snapshot --write-md .compair/scenario-a.md
"$COMPAIR_BIN" reports --file .compair/scenario-a.md
git restore --source=HEAD -- internal/api/capabilities.go
compair_seed_cleanup
```

Expected rationale:

- `compair_core` and `compair_cloud` publish `auth.single_user` and `features.activity_feed`
- the edited CLI file now expects `singleUser` and `activityFeed`
- Compair should treat this as an API-contract drift between the CLI and the backend capability schema

### Scenario B: `cross-product`

Seed a Core-vs-Cloud product-surface drift in `compair-cli` docs.

This change intentionally claims that Google OAuth is available on pure Core, even though the backend capabilities route and Cloud OAuth implementation keep that path Cloud-only.

```bash
compair_seed_setup scenario-b
cd "$SCENARIO_ROOT/compair-cli"

python3 - <<'PY'
from pathlib import Path

path = Path("docs/core_quickstart.md")
text = path.read_text()
old = "Google OAuth is a Cloud-only path and is not expected on Core."
new = "Google OAuth is available on Core and should appear in /capabilities when client credentials are configured."
if old not in text:
    raise SystemExit("expected text not found")
path.write_text(text.replace(old, new, 1))
print(f"Seeded temporary drift in {path}")
PY

git diff -- docs/core_quickstart.md
"$COMPAIR_BIN" sync --json --gate cross-product --snapshot-mode snapshot --write-md .compair/scenario-b.md
"$COMPAIR_BIN" reports --file .compair/scenario-b.md
git restore --source=HEAD -- docs/core_quickstart.md
compair_seed_cleanup
```

Expected rationale:

- `compair_core/server/routers/capabilities.py` only advertises `google_oauth` for the Cloud edition
- `compair_cloud` owns the Google OAuth routes
- `compair_desktop` self-hosted docs also describe Google OAuth as Cloud-only

### Scenario C: `review`

Seed a CLI docs-to-implementation drift in the API map.

This change intentionally documents the wrong activity endpoint, while the CLI client, Desktop client, and backend still use `/get_activity_feed`.

```bash
compair_seed_setup scenario-c
cd "$SCENARIO_ROOT/compair-cli"

python3 - <<'PY'
from pathlib import Path

path = Path("docs/api_mapping.md")
text = path.read_text()
old = "| `activity` | `GET /get_activity_feed` |"
new = "| `activity` | `GET /activity_feed` |"
if old not in text:
    raise SystemExit("expected text not found")
path.write_text(text.replace(old, new, 1))
print(f"Seeded temporary drift in {path}")
PY

git diff -- docs/api_mapping.md
"$COMPAIR_BIN" sync --json --gate review --snapshot-mode snapshot --write-md .compair/scenario-c.md
"$COMPAIR_BIN" reports --file .compair/scenario-c.md
git restore --source=HEAD -- docs/api_mapping.md
compair_seed_cleanup
```

Expected rationale:

- `compair-cli` still calls `/get_activity_feed`
- `compair_desktop` still calls `/get_activity_feed`
- `compair_core` still exposes `/get_activity_feed`

Current note:

- On the current OpenAI-backed local-Core paths, this scenario now lands as an exact-seed pass and correctly calls out `/get_activity_feed` vs `/activity_feed`.
- On the fully local no-key path, this scenario is still not reliable enough to use as a quality benchmark.

### Scenario D: `strict`

Seed a lower-friction website/install drift in `compair_site`.

This change intentionally makes the marketing copy claim the opposite of the live CLI install channels.

```bash
compair_seed_setup scenario-d
cd "$SCENARIO_ROOT/compair_site"

python3 - <<'PY'
from pathlib import Path

path = Path("src/content/site.ts")
text = path.read_text()
old = 'note: "Homebrew on macOS and Linux package repos are live. Windows is available from GitHub Releases while WinGet remains pending upstream approval.",'
new = 'note: "WinGet is live today. Homebrew on macOS and Linux package repos are still planned.",'
if old not in text:
    raise SystemExit("expected text not found")
path.write_text(text.replace(old, new, 1))
print(f"Seeded temporary drift in {path}")
PY

git diff -- src/content/site.ts
"$COMPAIR_BIN" sync --json --gate strict --snapshot-mode snapshot --write-md .compair/scenario-d.md
"$COMPAIR_BIN" reports --file .compair/scenario-d.md
git restore --source=HEAD -- src/content/site.ts
compair_seed_cleanup
```

Expected rationale:

- the site copy now contradicts the CLI README and user guide install matrix
- the download page and package-distribution docs still show Homebrew and Linux repos as live
- this is the kind of broader launch-surface mismatch that `strict` is meant to catch

Current note:

- This scenario remains the softest of the seeded set.
- OpenAI-backed local-Core runs can still surface the seeded install-note drift or another legitimate `strict`-worthy mismatch in the same surface area, but the exact classification is less stable than A, B, or C.
- Treat this as a useful launch-surface sanity check, not the primary benchmark for exact-seed precision.

### Manual severity/type threshold

If you want to exercise the low-level flags directly, rerun Scenario A and replace the preset with:

```bash
compair_seed_setup scenario-threshold
cd "$SCENARIO_ROOT/compair-cli"

python3 - <<'PY'
from pathlib import Path

path = Path("internal/api/capabilities.go")
text = path.read_text()
text = text.replace('json:"single_user"', 'json:"singleUser"', 1)
text = text.replace('json:"activity_feed"', 'json:"activityFeed"', 1)
path.write_text(text)
print(f"Seeded temporary drift in {path}")
PY

"$COMPAIR_BIN" sync --json --fail-on-severity high --fail-on-type potential_conflict --snapshot-mode snapshot --write-md .compair/scenario-threshold.md
"$COMPAIR_BIN" reports --file .compair/scenario-threshold.md
git restore --source=HEAD -- internal/api/capabilities.go
compair_seed_cleanup
```

Success criteria:

- advisory vs failing behavior matches the selected gate
- output is readable enough to paste into CI logs or PR artifacts
- each seeded drift is described as a cross-repo mismatch rather than a generic code-review comment
- `api-contract` feels safe as the first blocking policy for hard contract issues
- `strict` is the only preset you would reserve for broader launch-surface or integration-branch mismatches

### Failure handling

- `compair sync` exits non-zero because the gate matched the seeded drift: expected for blocking presets. Save the JSON/report output if you want a CI artifact, then restore the temporary change and continue.
- `No new changes for ...`: the command probably ran without `--snapshot-mode snapshot`, or the drift was already restored. Re-seed the example, confirm the file still differs in `git diff`, and rerun the same command.
- `No new feedback generated.`: the unchanged drift was likely analyzed already, or the current repo set did not produce a strong enough mismatch. Reseed or vary the drift, or run the scenario in a fresh validation group.
- `chunk task ... ended with status FAILURE`: run `compair doctor` for Cloud mode, or `compair core doctor` plus `docker logs compair-core --tail 200` for local Core. If `doctor` still shows a pending review task after a terminal backend failure, reruns may keep resuming that saved task; either wait for the pending-task stale cutoff or remove the `pending_task_*` fields from `.compair/config.yaml` before rerunning.
- `GET /load_groups ... Internal Server Error` or the hosted API OOMs during baseline tracking: retry only the failed `track` command after the service recovers; do not rerun the entire batch and create duplicate repo documents. If this reproduces on the optimized build, capture API memory graphs/logs and plan an API memory bump.
- Hosted Cloud failures that reproduce on an unmodified released build are worth escalating. Collect the failing task id, `compair doctor --json`, and any API/worker logs you have, then send that bundle to `support@compair.sh`.
- Auth, group, or repo-binding errors from `compair doctor` should be fixed before more sync attempts. The `Fix` text in `doctor` is the shortest path in those cases.

## 5. Release-channel validation

### Homebrew

```bash
brew update
brew reinstall --cask compair
compair version
```

### Linux package repo

```bash
curl -fsSL https://rocketresearch-inc.github.io/compair-packages/install/debian.sh | bash
compair version
```

### GitHub Release artifacts

```bash
mkdir -p /tmp/compair-release-smoke
cd /tmp/compair-release-smoke
# Download the latest platform archive from GitHub Releases, unpack it, then:
./compair version
```

### WinGet after upstream merge

```powershell
winget install RocketResearchInc.Compair
compair version
```

Success criteria:

- the live install paths match the README and download page
- version output matches the tagged release you expect
- package-manager installs do not require undocumented manual steps

## Suggested order

1. Install-path validation
2. First-run matrix
3. Real cross-repo suite review
4. Local CI simulation
5. Release-channel validation

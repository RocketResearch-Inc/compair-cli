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

### Local Core demo with OpenAI generation and local embeddings

```bash
export HOME="$(mktemp -d)"
compair core config set --generation-provider openai --embedding-provider local --openai-api-key "$OPENAI_API_KEY"
compair core up
compair demo --mode local
```

### Local Core demo with fully OpenAI-backed providers

```bash
export HOME="$(mktemp -d)"
compair core config set --provider openai --openai-api-key "$OPENAI_API_KEY"
compair core up
compair demo --mode local
```

Success criteria:

- Cloud asks for the expected login flow and then completes
- Local Core stays in single-user mode unless you explicitly switch auth
- The OpenAI-backed local demo produces natural feedback text instead of fallback reference dumps

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
- notifications add useful ranking/rationale instead of duplicating the report text when findings are present

## 4. Local CI simulation

Run one seeded drift at a time. Restore it before moving to the next scenario so each gate is tested against one clear change.

No commit is required for these validations. The commands below use `--snapshot-mode snapshot` so Compair reviews the current working tree, not just `LastSyncedCommit..HEAD`.

Important:

- One seeded drift usually produces one new feedback / notification cycle.
- Re-running different gate commands against the exact same unchanged drift will often show zero new feedback, because Compair avoids regenerating feedback for chunks that already have feedback, and the notification gate only evaluates fresh events from the current run.
- If you want to compare multiple gates, reseed or vary the drift between runs.
- A non-zero exit is expected when a blocking gate correctly matches the seeded drift. Treat that as a pass for the gate, then restore the file and move on.

### Scenario A: `api-contract`

Seed a temporary capabilities schema drift in `compair-cli`.

This change intentionally makes the CLI expect camelCase capability fields, while Core and Cloud still publish snake_case fields from `/capabilities`.

```bash
cd "$COMPAIR_ROOT/compair-cli"

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
compair sync --json --gate api-contract --snapshot-mode snapshot
git restore --source=HEAD -- internal/api/capabilities.go
```

Expected rationale:

- `compair_core` and `compair_cloud` publish `auth.single_user` and `features.activity_feed`
- the edited CLI file now expects `singleUser` and `activityFeed`
- Compair should treat this as an API-contract drift between the CLI and the backend capability schema

### Scenario B: `cross-product`

Seed a Core-vs-Cloud product-surface drift in `compair-cli` docs.

This change intentionally claims that Google OAuth is available on pure Core, even though the backend capabilities route and Cloud OAuth implementation keep that path Cloud-only.

```bash
cd "$COMPAIR_ROOT/compair-cli"

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
compair sync --json --gate cross-product --snapshot-mode snapshot
git restore --source=HEAD -- docs/core_quickstart.md
```

Expected rationale:

- `compair_core/server/routers/capabilities.py` only advertises `google_oauth` for the Cloud edition
- `compair_cloud` owns the Google OAuth routes
- `compair_desktop` self-hosted docs also describe Google OAuth as Cloud-only

### Scenario C: `review`

Seed a CLI docs-to-implementation drift in the API map.

This change intentionally documents the wrong activity endpoint, while the CLI client, Desktop client, and backend still use `/get_activity_feed`.

```bash
cd "$COMPAIR_ROOT/compair-cli"

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
compair sync --json --gate review --snapshot-mode snapshot
git restore --source=HEAD -- docs/api_mapping.md
```

Expected rationale:

- `compair-cli` still calls `/get_activity_feed`
- `compair_desktop` still calls `/get_activity_feed`
- `compair_core` still exposes `/get_activity_feed`

### Scenario D: `strict`

Seed a lower-friction website/install drift in `compair_site`.

This change intentionally makes the marketing copy claim the opposite of the live CLI install channels.

```bash
cd "$COMPAIR_ROOT/compair_site"

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
compair sync --json --gate strict --snapshot-mode snapshot
git restore --source=HEAD -- src/content/site.ts
```

Expected rationale:

- the site copy now contradicts the CLI README and user guide install matrix
- the download page and package-distribution docs still show Homebrew and Linux repos as live
- this is the kind of broader launch-surface mismatch that `strict` is meant to catch

### Manual severity/type threshold

If you want to exercise the low-level flags directly, rerun Scenario A and replace the preset with:

```bash
cd "$COMPAIR_ROOT/compair-cli"
compair sync --json --fail-on-severity high --fail-on-type potential_conflict --snapshot-mode snapshot
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

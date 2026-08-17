# Baseline run submission and status

`compair baseline run` is the opt-in document-level submission workflow for a
previously uploaded, ingested, and compatibly indexed immutable Git snapshot.
It never invokes or falls back to legacy retrieval. It does not accept query,
diff, prompt, evidence, or Feedback text as command-line arguments.

## Query-parity contract

The frozen study comparator uses the stdout bytes from:

```text
git diff HEAD^ HEAD --no-ext-diff
```

For the plan's immutable revisions, `raw_git_diff_v1` is the byte-equivalent
explicit-revision command:

```text
LC_ALL=C git diff <base_revision> <head_revision> --no-ext-diff
```

The scanner runs Git with isolated system/global configuration, disabled
external diff and text-conversion hooks, and a non-interactive environment.
It applies no full-index, binary-patch, rename/copy-detection, pathspec, or
newline normalization options. Tests cover ordinary changes, add/delete,
rename/copy candidates, mode-only changes, binary changes, SHA-1 and SHA-256
object formats, dirty worktrees, and mutable repository configuration.

## Commands

```text
compair baseline run \
  --group <group-id> \
  --plan <scan-input.json> \
  --index-result <baseline-index-result.json> \
  [--resume] [--wait] \
  [--timeout 20m] [--poll-interval 2s] [--json]

compair baseline run status \
  --group <group-id> \
  --job-id <run-job-id> \
  [--json]
```

Every fresh and resumed submission rescans the immutable revisions and checks
the scan/query identity against the successful `baseline-index-result.v1`.
The query exists only in process memory and in the protected HTTPS POST body.
Remote endpoints require HTTPS; loopback HTTP still requires the explicit
local-development allowance already used by snapshot and index commands.

`--wait` polls through `queued`, `running`, `references_persisted`, and
`retryable_failed`. `feedback_persisted` is success whether Feedback count is
zero or positive. `insufficient`, `terminal_failed`, `blocked`, and `cancelled`
are terminal non-success states. Automatic and manual dispatch are reported
as advertised; the CLI does not start an internal worker.

Exit codes are: 0 success or accepted/pending, 2 invalid input, 3
authentication/authorization, 4 capability unavailable/not ready, 5 stale or
conflicting identity, 6 retryable/timeout, 7 terminal/insufficient/blocked/
cancelled, 8 transport/server-contract failure, and 9 internal failure.

## Protected resume state

Safe resumable state is stored beneath:

```text
~/.compair/state/baseline-runs/
```

Files are atomically replaced, mode 0600, authenticated with the protected
installation secret, and rejected if symlinked or corrupt. State contains
only safe identities, hashes, counts, timestamps, and status. It never stores
query/diff bytes, ciphertext, nonce, key IDs, parent-processing secrets,
provider bodies, evidence/Feedback content, credentials, or idempotency keys.
An exact replay preserves Core's original payload expiry. Changed revisions or
lineage fail before resume.

## Preview remains separate

Run and status output is one `baseline-run-result.v1` JSON value and contains
no Feedback text. After successful completion, read ordered findings with:

```text
compair baseline preview --group <group-id> --job-id <run-job-id>
```

Zero-finding success returns an empty ordered Feedback array. The run command
does not call preview automatically. Generation provider/model fingerprints
remain available through the authorized preview provenance; the frozen v2
status response does not duplicate those fields, so their run-result fields
are nullable.

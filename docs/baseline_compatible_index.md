# Compatible baseline index from the CLI

`compair baseline index` is the opt-in Phase 2B2L.2C bridge from one successful
Phase 2B snapshot-upload result to Core's existing compatible-index continuation.
It does not scan repositories, upload content, build an index locally, submit a
baseline run, call preview, or invoke an internal Core worker.

## Submit and wait

```console
compair baseline index \
  --group <group-id> \
  --upload-result <baseline-snapshot-upload-result.json> \
  --wait --timeout 20m --poll-interval 2s --json
```

The group must be explicit and must exactly match the upload result. The input
must be a strict successful `baseline-snapshot-upload-result.v1` with complete
part counts and non-null continuation/corpus/generation identities. The CLI
reauthorizes and rechecks that continuation through Core before it uses the
server-returned corpus manifest and ingestion-provenance fingerprints.

Before any submission, the CLI calls
`POST /baseline/control/v2/capabilities`, requires the exact frozen v2 hash,
and independently requires `index_build` submission `safe`, endpoint
`authenticated_post`, and readiness `ready`. Baseline-run readiness is not a
prerequisite. The exact server-advertised index format, tokenizer, retrieval
configuration fingerprint, and embedding identity become the submitted frozen
v2 index intent. There are no model-setting flags.

Remote Core must use verified HTTPS. Local HTTP requires the explicit
`--allow-loopback-http` flag and the client verifies that the connected peer is
actually loopback.

## Resume and status

If a submission or wait is interrupted, repeat the exact command with
`--resume`. The CLI derives the same private idempotency and submission request
identities from the existing installation secret and immutable index intent.
It never stores those raw identities.

```console
compair baseline index \
  --group <group-id> \
  --upload-result <baseline-snapshot-upload-result.json> \
  --resume --wait --json

compair baseline index status \
  --group <group-id> \
  --job-id <index-job-id> \
  --json
```

Status is POST-only, reauthorizes each read, and does not claim work or extend
a lease. Polling uses bounded exponential backoff with jitter. In automatic
dispatch mode, Core's database worker can advance the job. Manual dispatch is
still a valid submission: `--wait` only observes while a trusted operator runs
the internal worker.

Protected state lives under
`~/.compair/state/baseline-indexes/`. It contains only safe IDs, hashes,
states, counts, protocol pins, and timestamps; it is HMAC-protected, written
atomically with restrictive permissions, and rejects symlinks. Successful
state is removed only after the final JSON value is written. Retryable state is
retained. Upload state and plan files are never removed by this command.

## Output and exit codes

Stdout is exactly one `baseline-index-result.v1` JSON value. Progress and
sanitized diagnostics use stderr. The result never includes credentials,
endpoint URLs, repository paths/remotes, source content, raw diffs/queries,
vectors, idempotency material, leases, or internal errors.

| Code | Meaning |
|---:|---|
| 0 | Submission/status read succeeded, including a non-waiting pending result |
| 2 | Usage or upload-result contract failure |
| 3 | Authentication or authorization failure |
| 4 | Capability unavailable or not ready |
| 5 | Idempotency conflict, stale generation, or identity mismatch |
| 6 | Timeout or retryable incomplete operation |
| 7 | Terminal failed, blocked, or cancelled server outcome |
| 8 | Transport/server protocol contract failure |
| 9 | Internal CLI failure |

The frozen v2 index-status schema has no `blocked` index state. Core therefore
must project a blocked index build as its frozen safe error or terminal-failed
status; the CLI rejects an unversioned `blocked` wire response rather than
silently expanding the contract.


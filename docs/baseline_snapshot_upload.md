# Baseline snapshot upload

Phase 2B2L.2B adds authenticated upload and optional ingestion-continuation polling. It does not provision repository registrations, submit an index build or baseline run, or change any non-baseline command.

## Command

```text
compair baseline upload \
  --group <group-id> \
  --plan <baseline-scanner-inputs.v1.json> \
  [--resume] [--wait] [--timeout 10m] [--poll-interval 2s] [--json]
```

Remote Core deployments require normal verified HTTPS. Local development additionally requires `--allow-loopback-http`; the URL must name loopback and the actual connected peer must be a loopback address. Redirects are rejected. The command requires existing CLI credentials and never uses `Host`, `Forwarded`, or `X-Forwarded-*` headers as authority.

The command always emits exactly one safe `baseline-snapshot-upload-result.v1` JSON value on stdout after an operational attempt. Safe stage/count progress is written to stderr. `--json` is an explicit alias for the already-default format. The plan path is read locally and is never sent or stored.

Exit codes are stable: `0` success, `2` usage, `3` authentication/authorization, `4` immutable repository/rescan mismatch, `5` contract/limit/conflict, `6` timeout or retryable incomplete work, `7` terminal server failure, and `8` internal failure.

## Sequence and replay

Every invocation performs capability preflight before Git scanning or server writes, then re-scans exact immutable Git objects. It performs `snapshot_begin`, sends content parts strictly by ordinal, performs `snapshot_commit`, and, with `--wait`, polls the existing sealed-snapshot continuation. It never caches raw diff or file content.

The scanner's `maximum_planned_upload_bytes` is a conservative one-attempt bound. Each real RFC 8785/JCS request is checked against its corresponding planned bound and frozen server limit before send. `transmitted_request_bytes` and `transmitted_request_count` report the exact request-body bytes consumed by the HTTP transport in the current CLI invocation; retries are included.

Core replay has two exactness requirements:

- `snapshot_begin` needs the original opaque idempotency key;
- an already accepted part needs the identical complete canonical body, including `request_id`.

The CLI therefore creates a random 32-byte installation secret and HMAC-derives the begin key plus stable mutation request UUIDs from the server identity, safe immutable plan identity, scan fingerprint, operation, and part ordinal. Opaque keys are never persisted, printed, or logged. Status request UUIDs remain fresh.

Retryable transport/5xx failures use bounded exponential backoff with bounded jitter. Authentication, authorization, schema, hash, manifest, intent, and idempotency conflicts are not retried. A lost response is recovered by sending the same canonical mutation request. `--resume` requires the retained state and an exact new scan match; it never silently starts another upload.

## Protected state

Locations beneath the resolved application root are:

```text
<application-root>/state/baseline-upload-install-secret.v1
<application-root>/state/baseline-uploads/<safe-plan-identity-sha256>.json
```

The application root defaults to `~/.compair` and may be securely isolated
with `COMPAIR_APP_DIR` as documented in [Config Reference](config_reference.md).

On POSIX systems directories are mode `0700` and files are mode `0600`; Windows uses the current user's protected profile ACL inherited by the existing CLI application directory. Existing symlinks, non-regular files, permissive POSIX secret/state files, and invalid HMACs are rejected. State writes use a same-directory temporary file, file flush, and atomic replace (`rename` plus directory fsync on POSIX; replace-existing/write-through `MoveFileEx` on Windows).

Resume JSON contains only its schema, group, safe server/plan/scan/revision fingerprints, protocol identity, snapshot/staging/continuation IDs, manifest hashes, counts, ordered part descriptors, completed ordinals/hashes, safe state, timestamps, and an integrity HMAC. It excludes local paths, remotes, diff/content, request bodies, credentials, tokens, opaque idempotency keys, and leases.

Successful output is written before state cleanup. Retryable interruptions retain state. Terminal expiry and unusable hash/intent/idempotency conflicts remove the per-upload state after safe output; the shared installation secret is retained because other uploads may depend on it.

Deleting or reinstalling the installation secret makes retained state unverifiable and prevents recovery of the original opaque replay identities. Another machine cannot resume unless the protected installation secret and matching resume state are securely transferred together. The CLI fails closed; it does not manufacture a new key for the same server snapshot. Operators must resolve an orphaned server staging record before explicitly starting a new upload.

## Polling boundary

`--wait` follows the authorized staging job into its separate ingestion continuation and waits through `queued`, `running`, and `retryable_failed`. It stops on `succeeded`, `terminal_failed`, `blocked`, `cancelled`, timeout, or authorization loss. Successful output may include only safe corpus/generation identities. Polling does not claim a worker, extend a lease/lifetime, submit an index, or make the corpus baseline-eligible.

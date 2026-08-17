# Compair baseline control-plane protocol v1

Status: staging subset implemented by Core in Phase 2B2L.1A; no CLI upload,
corpus ingestion, index build, or baseline run execution is enabled.

Protocol identifier: `baseline-control-plane.v1`

Machine-readable schema: `baseline-control-plane.v1.schema.json`

The protocol SHA-256 is the lowercase SHA-256 of this exact Markdown file.
It intentionally does not include itself as a literal. Core capabilities
return the deployed digest, and every request declares `protocol_version` plus
`protocol_sha256`; a mismatch fails before any state mutation.

This is the shared Core/CLI contract for a future trusted corpus snapshot,
compatible-index build, and baseline run. The copies under Core and Compair CLI
must remain byte-identical. Contract tests pin their SHA-256 and validate only
newly authored benign fixtures.

## Messages

Every message is a JSON object with
`protocol_version: "baseline-control-plane.v1"` and one fixed
`message_type`.

| Message | Direction | Purpose |
| --- | --- | --- |
| `scan_plan` | CLI local stdout only | Metadata-only result of future `--dry-run --json`; never sent automatically. |
| `snapshot_begin` | CLI to Core | Declare the complete metadata manifest and open bounded content staging. |
| `snapshot_content_part` | CLI to Core | Stage complete UTF-8 contents for supported files named by manifest ordinal. |
| `snapshot_commit` | CLI to Core | Seal all part hashes and request validation plus atomic activation. |
| `index_build_submit` | CLI to Core | Request an index for one active trusted snapshot and pinned embedding identity. |
| `run_submit` | CLI to Core | Submit one protected raw Git diff against an exact compatible publication. |
| `job_accepted` | Core to CLI | Return a durable opaque job ID and initial state. |
| `job_status_request` | CLI to Core | Request one authorized job status with IDs in the POST body. |
| `job_status` | Core to CLI | Return state, safe progress counts, and opaque result identifiers only. |
| `error` | Core to CLI | Return a typed safe code without input values or arbitrary exception text. |
| `capabilities_request` | CLI to Core | Request authenticated group-scoped capability state. |
| `capabilities` | Core to CLI | Report protocol/operation readiness and the frozen limits without URLs or credentials. |

The future write resources are:

| Operation | Method and path |
| --- | --- |
| Begin snapshot | `POST /baseline/control/v1/snapshots` |
| Stage content | `POST /baseline/control/v1/snapshots/{job_id}/parts` |
| Commit snapshot | `POST /baseline/control/v1/snapshots/{job_id}/commit` |
| Build index | `POST /baseline/control/v1/index-builds` |
| Submit run | `POST /baseline/control/v1/runs` |
| Read status | `POST /baseline/control/v1/jobs/status` |
| Read capabilities | `POST /baseline/control/v1/capabilities` |

Snapshot job IDs may appear as opaque path components for part/commit routing.
Group IDs, status job IDs, and capability selectors are POST-body fields, not
URL query strings. All resources above are authenticated POSTs with JSON
bodies. Phase 2B2L.1A implements begin/part/commit/status/capabilities only.

## Identity and Git contract

- `group_id` is explicit in every request. Baseline never reads or writes an
  implicit active group.
- The authenticated user identity comes only from Core's session. It is never
  accepted from a request field.
- Each repository has a durable Core-authorized `repository_id`, a safe
  single-component `repository_name`, and an exact lowercase Git object ID.
  Git SHA-1 (40 hex) and Git SHA-256 (64 hex) are supported.
- Exactly one changed repository is declared and it cannot also be a sibling.
  Every sibling revision is immutable and required.
- The changed repository declares `base_revision` and `head_revision`.
  The head must equal its repository revision and the base must be an ancestor
  of the head.
- The raw diff is the exact UTF-8 bytes from:

  ```text
  LC_ALL=C git diff <base_revision> <head_revision> --no-ext-diff
  ```

  This is the immutable-revision equivalent of the vendored comparator's
  `git diff HEAD^ HEAD --no-ext-diff`; it intentionally does not add binary
  patches, full object IDs, or explicit rename/copy detection. The scanner's
  isolated Git metadata and hardened environment neutralize mutable local and
  global configuration while preserving default Git diff semantics. Its
  representation is `raw_git_diff_v1`. No newline, Unicode, or path
  normalization is applied to the diff. `byte_size` and `sha256` cover the
  exact UTF-8 bytes. An empty diff is invalid for `run_submit`.
- Local repository paths, remote URLs, and Git credentials are scanner inputs
  only and are absent from every protocol message.

## Complete snapshot manifest

The scanner enumerates immutable Git trees, not the mutable checkout:
`git ls-tree -r -z --full-tree <revision>`, followed by bounded streaming
`git cat-file` reads. It never follows working-tree links.

Every sibling Git-tree entry produces one file record, including entries that
cannot become candidates. The record contains:

- one-based `ordinal`;
- repository ID, name, and revision;
- normalized POSIX `relative_path`;
- Git mode and object ID;
- file state, compatible skip reason, byte size, and SHA-256 where a blob
  exists;
- `content_required=true` only for a supported UTF-8 regular file.

File states and required skip reasons are:

| State | Skip reason | Behavior |
| --- | --- | --- |
| `supported` | null | Complete UTF-8 content is staged. |
| `unsupported_utf8` | `non_utf8` | Metadata only; never indexed. |
| `oversized` | `oversized` | Metadata only; never indexed. |
| `symlink_rejected` | `symlink` | Git mode `120000`; target is never followed. |
| `excluded` | `excluded_directory` or `unsupported_file_type` | Metadata only; never indexed. |
| `unreadable` | `unreadable` | Scan fails closed unless the immutable blob metadata can still be recorded. |

Excluded directory components are exactly `.git`, `.compair`, `build`,
`dist`, and `node_modules`. A Git submodule (`160000`) is
`excluded/unsupported_file_type`. File extensions are otherwise unrestricted.
Classification precedence is Git object type (symlink/submodule/unsupported
mode), excluded directory, byte-size limit, then UTF-8 validity. This makes a
file that violates more than one rule deterministic. A symlink blob's own
size/hash may be recorded as metadata, but its target string is never resolved.

Paths must be valid UTF-8, NFC-normalized, nonempty POSIX-relative strings.
Absolute paths, backslashes, NUL, drive prefixes, empty components, `.`,
`..`, repeated separators, trailing separators, or any string whose POSIX
clean form differs are fatal scan errors. The scanner rejects duplicate raw
repository/path pairs and duplicate NFC path aliases; it never silently
normalizes an alias.

Repositories sort by `(repository_name, repository_id)`. Files sort by
`(repository_name, relative_path, repository_id)`, and ordinals must match
that order.

### Canonical hash and snapshot identity

The canonical manifest value contains exactly:

```json
{
  "schema_version": "baseline-snapshot.v1",
  "changed_repository": {},
  "sibling_repositories": [],
  "files": []
}
```

Repository and file objects contain every corresponding field from the schema;
arrays use the ordering above. The value is serialized with RFC 8785 JSON
Canonicalization Scheme and hashed as UTF-8 with SHA-256. The lowercase digest
is `canonical_manifest_hash`; `snapshot_id` is
`bsnap_<canonical_manifest_hash>`. Neither `group_id`, the two derived hash
fields, local paths, nor raw content enters this hash.

## Frozen limits and staging

All byte limits are decimal bytes and are checked after UTF-8 encoding.

| Limit | Value |
| --- | ---: |
| Sibling repositories | 128 |
| File records | 50,000 |
| One file | 200,000 |
| Total supported contents | 512,000,000 |
| Raw Git diff | 8,000,000 |
| Snapshot manifest request body | 32,000,000 |
| Encoded content-part request body | 8,000,000 |
| Decoded content in one part | 1,000,000 |
| File items in one part | 1,000 |
| Content parts | 512 |
| Other control request body | 64,000 |
| Staging lifetime after last activity | 24 hours |
| Safe terminal job metadata retention | 30 days |

Supported files are packed whole, in file-ordinal order, using a deterministic
greedy algorithm: add the next file until doing so would exceed either the
1,000,000-byte or 1,000-item part limit, then start the next one. Files are
never split. Part ordinals are consecutive from one. `part_sha256` covers
the RFC 8785 canonical `content_items` array. Commit
`content_manifest_hash` covers the ordered array of
`{part_ordinal,part_sha256}`.

`snapshot_begin` stores an incomplete staging declaration only.
`snapshot_content_part` retries with the same job, ordinal, hash, and bytes
are idempotent; a different body is a conflict. `snapshot_commit` succeeds
only when every supported ordinal appears exactly once, every hash/size
matches, every repository/file count matches, and the complete manifest is
valid. In Phase 2B2L.1A commit atomically seals staging only. It never creates
or activates a corpus generation or index publication. A future L.1B worker
must reauthorize and revalidate the sealed session before atomic ingestion;
open, expired, failed, or merely sealed staging is not baseline-eligible.

## Idempotency and asynchronous jobs

- `request_id` is a random UUID for correlation.
- `idempotency_key` is a caller-generated opaque 32–128 character token. It
  must not be a query/content hash, timestamp, path, or repository name.
- Core scopes uniqueness to `(group_id, operation, idempotency_key)` and
  computes a private intent hash over the canonical request.
- An identical replay reauthorizes current access and returns the prior
  `job_id`. A conflicting replay returns HTTP 409 with
  `idempotency_conflict` and performs no write.
- `job_id` is a server-generated UUID. Job states are `queued`, `running`,
  `succeeded`, `retryable_failed`, `terminal_failed`, and `cancelled`.
  Phase 2B2L.1A persists transactional leases, expiry, and attempt recovery;
  no ingestion or index worker consumes those leases yet.
- Status contains only counts, hashes/fingerprints, opaque IDs, state, attempt
  number, timestamps, and a typed error code. It contains no idempotency key,
  raw diff, file content, source text, repository path, request body, provider
  body, credential, or internal endpoint.

Index publication follows the existing fail-closed lifecycle: build all
lexical/dense artifacts for the still-active complete generation, validate all
fingerprints and counts, then move the compatible publication pointer in one
transaction. A failure or concurrent activation leaves the prior publication
unchanged.

`run_submit` binds the explicit group, authoritative source document/chunk,
changed repository, snapshot generation/hash, compatible index publication,
parent processing key, and raw diff base/head/hash. Core reauthorizes and
revalidates them before queueing and again before retrieval/persistence.

## CLI scanner contract

The future opt-in command is:

```text
compair baseline scan \
  --group <group-id-or-name> \
  --changed-spec <baseline-changed-repository.v1.json> \
  --sibling-spec <baseline-sibling-repository.v1.json> [...] \
  --base <exact-git-revision> --head <exact-git-revision> \
  --dry-run --json
```

`--group`, `--changed-spec`, at least one `--sibling-spec`, `--base`,
`--head`, `--dry-run`, and `--json` are all required. Repository spec
files contain the local path plus protocol repository ID/name/revision; local
paths are removed from output. Baseline scanning never consults the active
group, never auto-selects a group, and never uploads or calls Core in
Phase 2B2L.2 dry-run mode.

The changed spec is exactly:

```json
{
  "schema_version": "baseline-changed-repository-input.v1",
  "local_path": "/local/scanner-only/path",
  "repository_id": "registered-repository-id",
  "repository_name": "safe-name",
  "repository_revision": "exact-lowercase-git-object-id",
  "source_document_id": "authoritative-core-document-id"
}
```

Each sibling spec is exactly:

```json
{
  "schema_version": "baseline-sibling-repository-input.v1",
  "local_path": "/local/scanner-only/path",
  "repository_id": "registered-repository-id",
  "repository_name": "safe-name",
  "repository_revision": "exact-lowercase-git-object-id"
}
```

Unknown fields are rejected. `local_path` is a scanner trust-boundary input,
may be absolute locally, and never enters a message, hash, diagnostic, or
error. The changed revision must equal `--head`. All revisions are resolved to
40- or 64-character lowercase object IDs before enumeration; symbolic refs are
not serialized. Changed/sibling repository IDs must be disjoint and sibling
IDs and `(name,id)` pairs must be unique.

Dry-run stdout is exactly one `scan_plan` JSON value. Diagnostics go only to
stderr. It includes metadata and hashes, not raw file contents or raw diff
text. A fatal path, identity, Git, completeness, or limit error produces no
JSON plan.

## Core authorization and transport

Future write APIs require Core's authenticated session and an explicit group.
For every initial request and replay Core verifies:

1. caller membership and authorization for the group;
2. registered repository IDs/names and the changed/sibling roles;
3. source document/chunk ownership by that group;
4. exact immutable revisions and base/head relationship;
5. group-scoped corpus generation and compatible publication;
6. current embedding/index capability and protected query transport.

Remote writes require authenticated HTTPS with certificate verification and
TLS 1.2 or newer. Plain HTTP is permitted only when an explicit development
override is enabled and both peer and advertised host are loopback. The raw
diff additionally uses the existing retrieval-query broker policy:
authenticated certificate-verified broker TLS remotely, or its narrow explicit
loopback/test override. Task argument/event representations remain redacted.
Core fails before staging or dispatch when these requirements are unavailable.

Proxies, ASGI access logs, application logs, traces, Celery events, task status,
and errors must not record request bodies. Authentication headers are always
redacted. Body hashes and byte counts are safe.

## Structured errors

`error` responses expose only a request ID, HTTP status, stage, retryability,
and one code from the schema. No arbitrary provider, database, Git, path,
query, or source exception text is returned. Relevant HTTP mappings are:

- 400/422: malformed contract or limit violation;
- 401: missing/expired authentication;
- 404: privacy-safe absent or unauthorized group/repository/source/job;
- 409: idempotency conflict, stale snapshot, or stale publication;
- 413: body or declared payload limit;
- 429: bounded admission control;
- 503: transport, embedding, or worker capability unavailable.

## Security review

### Preview URL logging

The existing read-only preview uses
`GET /baseline/preview/v1?group_id=...&run_id=...` or `digest_id`. It does
not put query/evidence text in the URL, but default access logs may retain
group/run/digest identifiers. Deployments should disable query-string logging
for this route, apply `Cache-Control: no-store` and
`Referrer-Policy: no-referrer`, and restrict logs like other authorization
metadata. Moving selectors is not part of this phase.

### Request size and parsing

Ingress rejects oversized bodies before JSON parsing. Declared counts/sizes are
checked before staging, streamed content is bounded independently, and Core
recomputes byte counts and hashes. Compression cannot bypass decompressed
limits; request compression is disabled for these endpoints in v1.

### Replay and concurrency

Opaque idempotency keys, private intent hashes, part hashes, transactional
leases, and current-authorization checks make retries deterministic. A replay
never revives expired staging or bypasses a newer active corpus/publication.

### Retention and deletion

Incomplete staging content expires 24 hours after last activity. Raw diffs
exist only for the protected request/task lifetime and are excluded from result
backends. Safe terminal job metadata expires after 30 days. Active and prior
corpus/index retention follows the existing explicit retention policy; group
deletion cascades all group-scoped staging, jobs, corpus, index, and evidence.
Source deletion cancels pending work and prevents new References while
preserving already-auditable baseline evidence under the approved bridge
policy. Non-group historical evidence deletion still requires the explicit
authorized retention purge.

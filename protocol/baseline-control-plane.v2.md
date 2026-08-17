# Compair baseline control-plane protocol v2

Status: frozen design contract only. Core exposes no v2 production endpoint in
Phase 2B2L.1D.0. The valid capability fixture therefore advertises both v2
operations as unavailable.

Protocol identifier: `baseline-control-plane.v2`

Machine-readable schema: `baseline-control-plane.v2.schema.json`

The protocol SHA-256 is the lowercase SHA-256 of this exact Markdown file. It
intentionally does not include itself as a literal. Every request and response
declares `protocol_version` and `protocol_sha256`. Core must reject a mismatch
before authentication-derived authorization is used for a write and before any
durable state is created. A v2 request is never reinterpreted as v1.

This is the shared Core/Compair CLI contract for compatible-index submission
and protected document-level baseline-run submission. The specification,
schema, and fixture copies under Core and Compair CLI must remain
byte-identical.

## Compatibility boundary

`baseline-control-plane.v1` remains frozen byte-for-byte. Its implemented
subset is staging and status only. Its v1 capability response continues to
advertise `index_build=unavailable` and `baseline_run=unavailable`; a v1 client
cannot opt into v2 by sending v2 fields.

V2 does not reopen, upload, or reinterpret v1 staging. It references the
opaque IDs and safe fingerprints of a successful, immutable ingestion
continuation created by the existing internal continuation lifecycle.

## Transport and parsing

All resources are authenticated POST endpoints with `application/json` UTF-8
bodies. Remote requests require verified HTTPS. Plain HTTP is accepted only
under the existing explicit local-development exception when the actual
connected peer and bind address are loopback; advertised `Host`, `Forwarded`,
and `X-Forwarded-*` headers do not establish loopback. A trusted TLS proxy must
be explicitly allowlisted and must overwrite forwarding headers.

Before JSON Schema or authorization processing, Core must enforce the frozen
control-plane parsing rules: body byte limit, exact media type, valid UTF-8,
duplicate-key rejection at every object depth, and rejection of NaN, Infinity,
and -Infinity. JSON strings are carried directly; base64, form, multipart,
content encoding, and URL/query-string representations are forbidden.

Request bodies and protected fields are excluded from access/application logs,
traces, task status, exception strings, and error responses. Reverse proxies
must disable request-body logging and redact URL query strings even though v2
defines no query-string input.

## Endpoint and message inventory

| Operation | Method and path | Request | Success response |
| --- | --- | --- | --- |
| Capabilities | `POST /baseline/control/v2/capabilities` | `capabilities_request` | `capabilities` |
| Submit compatible index | `POST /baseline/control/v2/index-builds` | `index_build_submit` | `job_accepted` |
| Read index status | `POST /baseline/control/v2/index-builds/status` | `job_status_request` | `job_status` |
| Submit baseline run | `POST /baseline/control/v2/runs` | `run_submit` | `job_accepted` |
| Read baseline-run status | `POST /baseline/control/v2/runs/status` | `job_status_request` | `job_status` |

Every request carries a random `request_id`, explicit `group_id`, exact
protocol version, and exact protocol hash in its JSON body. Status job IDs are
also body-only. There are no public worker claim, lease, reclaim, or completion
messages. `error` is the only failure response shape.

An operation whose capability `submission` is `unavailable` must return HTTP
503 `capability_unavailable` before any job/idempotency write. A `safe`
submission value is truthful only when the authenticated POST endpoint is
implemented and authorization is enforced. `dispatch` independently reports
`automatic`, `manual`, or `unavailable`; it never exposes internal lease state.
`readiness` independently reports `ready`, `not_ready`, or `unavailable`.

## Compatible-index submission

`index_build_submit` binds one request to:

- the explicit authorization group;
- the exact successful ingestion continuation;
- the active immutable corpus generation;
- the corpus manifest and ingestion-provenance fingerprints;
- `baseline-index.v1` and `baseline_v1_frozen_tokenizer.v1`;
- the complete retrieval configuration fingerprint; and
- one pinned `baseline-embedding-http.v1` identity, including provider, model,
  immutable revision, dimension, `float32` dtype, and identity fingerprint.

The caller supplies an opaque 32–128 character `idempotency_key`. Core scopes
it to `(group_id, index_build)` and hashes the canonical intent privately. The
same key plus byte-equivalent semantic intent reauthorizes and replays the
same job. The same key with any different intent returns HTTP 409
`idempotency_conflict` and writes nothing. Neither the key nor its private
intent hash is returned.

Submission does not imply automatic dispatch. A future worker must revalidate
the active complete trusted generation, ingestion provenance, repository
approvals, source authorization, embedding attestation, and configured index
identity at claim and immediately before the existing atomic publication
transaction. Public status contains safe identifiers, fingerprints, counts,
state, and a sanitized reason code only.

## Protected baseline-run submission

`run_submit` carries:

- explicit `group_id`;
- authoritative `source_document_id`;
- the approved opaque `changed_repository_registration_id`;
- caller-generated opaque `idempotency_key`;
- exact compatible index-publication identity;
- explicit `retrieval_query` in `raw_git_diff_v1` representation with declared
  base/head Git revisions, exact UTF-8 byte size, and SHA-256; and
- exact protocol version/hash plus random caller `request_id`.

One `run_submit` targets exactly one authoritative `source_document_id`.
`changed_repository_registration_id` identifies that document's approved
changed repository. The retrieval query is the complete Git diff/change set,
not one source chunk or a query derived from a source chunk.

The control-plane run invokes `baseline_v1` retrieval exactly once for the
complete query. The existing `process_document` per-chunk fan-out is not part
of this path: no source chunk controls query construction, candidate discovery,
ranking, evidence persistence, generation, or the aggregate outcome. Existing
legacy and non-control-plane per-chunk workflows remain unchanged.

The server creates one random parent processing secret after authorization and
acceptance. It derives one document-level persistence identity from that
secret and the immutable run intent. Clients cannot supply either value. The
accepted response may expose only a distinct opaque `processing_run_id`, never
the parent secret or persistence identity.

Exact replay of the same group-scoped idempotency key and byte-equivalent
semantic intent returns the same job, processing-run identity, retrieval run,
ordered evidence, References, and Feedback identities. Reuse of the key with
a different intent is `idempotency_conflict` and creates no effects.

The raw query exists only in this protected POST body and the bounded in-memory
Phase 2A `RetrievalRequest`. It never appears in a URL, CLI argument, status,
capability, log, trace, error, returned idempotency hash, provider metadata, or
safe fixture. Status reports only its SHA-256, Unicode code-point length,
UTF-8 byte size, and `origin=explicit`. The server verifies these values from
the received string; caller declarations are not trusted.

`raw_git_diff_v1` is byte-for-byte the hardened immutable-revision equivalent
of the vendored comparator's `git diff HEAD^ HEAD --no-ext-diff`: the scanner
runs `LC_ALL=C git diff <base_revision> <head_revision> --no-ext-diff` in its
isolated Git environment. It does not add binary patches, full object IDs, or
explicit rename/copy detection, and mutable worktree/configuration state cannot
alter a resumed query.

The byte limit is 8,000,000, matching the already frozen v1 raw Git diff limit
while bounding the formerly unbounded in-memory Phase 2A query field. The
complete encoded run request is limited to 8,100,000 bytes. JSON Schema's
`maxLength` is only an early code-point bound; strict processing must enforce
the UTF-8 byte limit, declared byte equality, and exact SHA-256 before dispatch.
An empty or whitespace-only query is invalid.

At acceptance and every retry, Core reauthorizes the caller/group/source,
changed-repository approval, active trusted corpus, and exact compatible
publication. Baseline retrieval is `baseline_v1` only. Insufficient, error,
stale, incompatible, or unauthorized retrieval has no legacy fallback.

## Document-level retrieval and persistence

One control-plane run produces at most one `baseline_retrieval_run`, one
ordered selected-evidence set, one ordered group of 1–4 `Reference` rows, one
generation lifecycle, and its ordered Feedback findings. New control-plane
baseline persistence does not require or manufacture a `source_chunk_id`.
References target `baseline_selected_evidence`; the authoritative
`source_document_id` and group scope remain mandatory durable provenance.

The retrieval algorithm remains the frozen comparator: exact full-corpus BM25
with `k1=1.5` and `b=0.75`, one pinned BGE dense lane using float32 dot
products, equal-weight RRF with `k=60`, and deterministic repository/path
ties. Retrieval ranks the complete eligible sibling corpus, takes the fused
top six, and performs filtering, content deduplication, and refill only within
those six. The single job-wide evidence budget is at most four unique items
and at most 16,000 selected-content characters. There is no per-chunk budget,
child retrieval, reranking, fallback, or aggregation step.

## Run status and effects

The frozen public run states and CLI-oriented classifications are:

| State | Terminal | Exit class | Permitted durable effects |
| --- | --- | --- | --- |
| `queued` | no | `pending` | none |
| `running` | no | `pending` | none; after the single ordered evidence/Reference transaction commits, use `references_persisted` |
| `references_persisted` | no | `pending` | one retrieval run, one evidence set of 1–4 items/at most 16,000 characters, and 1–4 ordered References; no Feedback |
| `feedback_persisted` | yes | `success` | generation completed successfully and its Feedback outcome was durably resolved; the same retrieval run and 1–4 ordered References remain, with 0–4 ordered Feedback rows |
| `insufficient` | yes | `insufficient` | none |
| `retryable_failed` | no | `pending` | stage-dependent, accurately counted; retry resumes from the last durable boundary |
| `terminal_failed` | yes | `failed` | stage-dependent, accurately counted; no future effects |
| `blocked` | yes | `blocked` | stage-dependent, accurately counted; no future effects |
| `cancelled` | yes | `cancelled` | stage-dependent, accurately counted; no future effects |

The exit class is protocol data, not an implemented CLI exit code. Phase
2B2L.1D.1 must map it without changing the established preview command's exit
codes. Polling should continue only for `pending`.

`retrieval_status` is `pending`, `ok`, `insufficient`, or `error`. An
`insufficient` public state requires `retrieval_status=insufficient`, a typed
reason code, and zero evidence, Reference, Feedback, generation, and
notification effects. Any non-OK retrieval similarly writes none of those
objects. `references_persisted` and `feedback_persisted` require
`retrieval_status=ok`, exactly one non-null `persisted_run_id`, equal positive
job-wide evidence/Reference counts, and internal enforcement of the advertised
16,000-character cap. A public run has no child-run manifest or per-chunk
outcome list.

For `feedback_persisted`, `generation_invoked` is true even when the compatible
review produces no findings. A zero-finding success has `feedback_count=0`,
`notification_outbox_count=0`, no Feedback rows, and no placeholder, empty,
synthetic, or `NONE` Feedback. The state means that generation completed and
the Feedback outcome was durably resolved; it does not assert that a Feedback
row exists. A positive finding count remains bounded by the Reference count
and preserves provider order.
The control-plane audit job itself remains durable for every outcome, including
insufficient, failed, blocked, and cancelled states. Notifications are not
part of run completion; status reports only an outbox count if a later
authorized phase creates privacy-safe digest entries.

## Safe capability and status fields

Capabilities report only supported protocol version/hash pairs, per-operation
submission/endpoint/dispatch/readiness state, frozen request/query limits,
transport policy, and required index/embedding identities. They contain no
endpoint URL, credential, repository/path, query, source text, idempotency key,
lease, or internal worker detail.

Index status may return corpus generation, ingestion continuation, publication,
manifest/config/embedding fingerprints, document/vector counts, and safe
timestamps/reason codes. Run status may additionally return the server-created
processing run ID, source document ID, changed-registration ID, persisted run
ID, ordered Reference/Feedback counts, and query provenance. It never returns
raw evidence, rendered evidence, findings, raw model bodies, or notification
content.

Errors contain a fixed code, stage, retryability, and HTTP status only. They do
not echo invalid values or arbitrary exception text. Privacy-safe 404/403
handling must not reveal whether an unauthorized group, document,
registration, generation, publication, or job exists.

## Frozen limits

All byte limits are decimal UTF-8/encoded JSON bytes and are checked before
buffering or semantic processing.

| Limit | Value |
| --- | ---: |
| Index submission/status/capability request body | 64,000 |
| Raw retrieval query | 8,000,000 |
| Complete run submission body | 8,100,000 |
| Idempotency key | 32–128 ASCII characters |
| Selected evidence/References per run | 4 |
| Selected evidence characters per run | 16,000 |
| Feedback findings per run | 0–4 |
| Safe terminal status retention | 30 days |

## Freeze and implementation boundary

The JSON Schema is Draft 2020-12 and closes every object with
`additionalProperties=false`. Valid fixtures use newly authored synthetic
identities and benign diff text. Invalid fixture recipes cover protocol/hash
mismatch, duplicate keys, non-finite numbers, raw-query overflow, invalid state
combinations, absent authorization identities, and protected-field leakage.

Phase 2B2L.1D.0 adds no endpoint or runtime feature. Production enablement
requires a migration-owned run-submission job, authenticated authorization and
transport guards, private idempotency storage, lease-safe dispatch, the
existing persistent retriever/persistence/generation services, safe status
rendering, fault-injection tests, and an independently gated CLI implementation.

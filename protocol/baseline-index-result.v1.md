# Baseline compatible-index CLI result v1

Schema identifier: `baseline-index-result.v1`

Machine-readable schema: `baseline-index-result.v1.schema.json`

This is the single-JSON stdout contract for `compair baseline index` and
`compair baseline index status`. It is a safe projection of the frozen
`baseline-control-plane.v2` compatible-index operation. It never claims or
executes a worker.

A submission result binds the explicit group, Phase 2B scan fingerprint,
authenticated ingestion continuation, trusted corpus generation, corpus
manifest and provenance fingerprints, and exact server-advertised index
intent. A direct status read cannot reconstruct local upload fields, so those
fields are JSON `null`, never inferred from repository names or paths.

The protocol pin is `baseline-control-plane.v2` with SHA-256
`b278abe007779f05e92509db068f555701c03cba5cf236151e8df231a9b44091`.
The index intent fixes `baseline-index.v1`,
`baseline_v1_frozen_tokenizer.v1`, the frozen BM25/dense/equal-RRF-60
configuration fingerprint, and pinned embedding fingerprint.
The separate index-intent fingerprint is the SHA-256 of the exact canonical
server-advertised intent used for submission.

`dispatch_mode` reports `automatic` or `manual`; manual means a trusted
operator must run Core's internal worker. A publication and index fingerprint
are exposed only for `succeeded`. `retryable_incomplete` is a client timeout
or interruption with protected state retained.

Only safe IDs, hashes, counts, states, timestamps, and sanitized reason codes
are allowed. Credentials, endpoint URLs, paths/remotes, content, diffs/queries,
vectors, idempotency material, leases, and internal errors are forbidden.

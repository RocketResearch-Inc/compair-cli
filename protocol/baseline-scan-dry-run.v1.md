# baseline-scan-dry-run.v1

Status: frozen public CLI output contract for deterministic local planning. This contract contains metadata only; it is not an upload request and cannot authorize a repository.

## Encoding and ordering

`compair baseline scan --dry-run --json` emits exactly one UTF-8 JSON object followed by one LF. Object members are emitted in this order: `schema_version`, `protocol_version`, `protocol_sha256`, `group_id`, `changed_repository`, `sibling_repositories`, `snapshot_id`, `canonical_manifest_hash`, `scan_plan_jcs_sha256`, `content_manifest_hash`, `counts`, `skip_reason_counts`, `raw_diff`, `parts`, `manifest_request_bytes`, `commit_request_bytes`, `maximum_planned_upload_bytes`, `scan_fingerprint`, `warnings`, `errors`. Nested object order is the order declared by the schema. Arrays preserve scanner order. Hash inputs use RFC 8785/JCS, where object member order is lexical and therefore independent of display order.

All hashes are lowercase SHA-256 hex. Paths, raw Git diff bytes, file contents, repository local paths/remotes, credentials, tokens, idempotency keys, lease tokens, request bodies, and server endpoints are forbidden. `warnings` is exactly `["dry_run_only","no_network_or_persistence"]`; `errors` is empty for an emitted successful report.

## Counts and descriptors

`repository_count` equals the sibling repository count; the changed repository is separately identified and is not part of the sibling corpus count. `file_count = supported_file_count + skipped_file_count`. The six skip-reason counters sum to `skipped_file_count`. Parts are in strictly ascending, gap-free `part_ordinal` order beginning at one. Part `file_count` totals `supported_file_count`; part `decoded_content_bytes` totals `supported_content_bytes`. Zero supported files produce an empty parts array.

The part hash is SHA-256 of the RFC 8785/JCS array of that part's ordered content-item descriptors and contents. `content_manifest_hash` is SHA-256 of the JCS ordered array of `{part_ordinal,part_sha256}`. `canonical_manifest_hash`, `scan_plan_jcs_sha256`, `snapshot_id`, and `scan_fingerprint` follow `baseline-control-plane.v1` and the deterministic scanner contract.

## Planned versus transmitted bytes

`manifest_request_bytes` is a conservative maximum computed with the server's maximum 128-character begin idempotency key. Part and commit estimates use fixed-width UUID placeholders. `maximum_planned_upload_bytes` is exactly `manifest_request_bytes + commit_request_bytes + sum(parts[].request_bytes)`. It is a one-attempt conservative plan, not a claim about bytes sent across retries.

Real upload JCS-encodes each request, rejects it before sending if it exceeds its corresponding planned bound or frozen server limit, and separately reports `transmitted_request_bytes` for the current CLI invocation. Retries can make that exact invocation total exceed `maximum_planned_upload_bytes`.

## Semantic validation

The JSON Schema freezes shape and primitive bounds. Implementations must additionally enforce the cross-field count, sum, ordinal, hash-shape, warning, and error invariants above. Unknown members are rejected.

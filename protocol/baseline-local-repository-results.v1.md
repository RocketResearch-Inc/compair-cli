# baseline-local-repository-results.v1

This CLI-only contract freezes safe machine-readable results for local
repository operations and scan-plan creation. It is not sent to Core and does
not grant authorization.

`baseline-local-repository-result.v1` contains the operation, explicit group,
opaque registration, active state, nullable source-document ID, descriptor
hash, optional local binding ID, optional local path/Git sanity hashes, and
replay flag. `baseline-local-scan-plan-result.v1` contains the explicit group,
opaque changed/sibling registrations, pinned base/head commits, output-path
hash, plan hash, and timestamp.

The results never contain a local path, repository UID, remote URL, credential,
source content, diff, query, evidence, private idempotency key, or lease token.
The plan file itself necessarily contains local paths for the existing scanner,
is written privately, and is not this automation-safe result.

# `baseline-run-result.v1`

`compair baseline run` and `compair baseline run status` emit exactly one
UTF-8 JSON object followed by one LF. The result is a privacy-safe projection
of one document-level `baseline-control-plane.v2` run job. It never contains
the raw retrieval query, source/evidence content, Feedback text, provider
request/response bodies, credentials, endpoints, idempotency material,
encryption metadata, parent-processing secrets, or worker leases.

The protocol identity is `baseline-control-plane.v2` with SHA-256
`b278abe007779f05e92509db068f555701c03cba5cf236151e8df231a9b44091`.
The query fields are provenance only: SHA-256, Unicode code-point length,
UTF-8 byte size, and `origin=explicit`. One result identifies at most one run
job, one processing run, one persisted retrieval run, and job-wide counts of
at most four evidence items, References, and Feedback findings.

`references_persisted` is nonterminal. `feedback_persisted` is successful for
both zero and positive Feedback counts; findings remain advisory and do not
change the CLI exit class. Zero findings require generation invoked, positive
equal evidence/Reference counts, zero Feedback/outbox counts, and no synthetic
Feedback. `insufficient` has zero retrieval, persistence, generation, Feedback,
and outbox effects.

Generation provider/model/version and input/output fingerprint fields are
reserved nullable safe fields. The frozen v2 job-status response does not
expose them, so this CLI does not make an extra preview read to populate them.
After `feedback_persisted`, use the separately authorized
`compair baseline preview --group <group-id> --job-id <run-job-id>` command for
ordered Feedback and complete safe generation provenance. Run submission and
status never duplicate preview behavior.

`dispatch_mode` truthfully reports `automatic`, `manual`, or `unavailable`.
Successful resume state is deleted only after this JSON value is written.
Retryable state is retained. The public result never exposes the protected
resume-state identity or HMAC.


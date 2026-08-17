# `baseline-preview.v1`

`baseline-preview.v1` is the authenticated, read-only result contract for one
terminal document-level baseline control job. The contract is unreleased and
replaces the earlier draft in place. Its authoritative identity is
`baseline_control_run_job.job_id`; a retrieval-run ID is never a request
selector.

## Endpoint and transport

```text
POST /baseline/preview/v1
Content-Type: application/json; charset=utf-8
```

The body is limited to 4,096 bytes and must be strict UTF-8 JSON. Duplicate
object keys, non-finite numbers, request compression, extra fields, and more
than one JSON value are rejected before authorization. Remote requests require
verified HTTPS. Plain HTTP is accepted only through the existing explicit
actual-peer loopback exception; forwarded transport headers are trusted only
from an explicitly allowlisted immediate proxy.

The obsolete GET/query-string contract and `run_id` selector are unsupported.
The endpoint is authenticated and request/response bodies must not be logged.

## Request

Exactly one selector is required:

```json
{
  "schema_version": "baseline-preview.v1",
  "request_id": "00000000-0000-4000-8000-000000000001",
  "group_id": "00000000-0000-4000-8000-000000000002",
  "job_id": "00000000-0000-4000-8000-000000000003"
}
```

or replace `job_id` with `digest_id`. A digest is only an alternate lookup for
the same control job; it never becomes the authoritative run identity.

## Success response

A response is available only when the authorized control job is terminal
`feedback_persisted`, its one linked retrieval run is successful, its evidence
and References are intact, and its generation outcome is durably resolved.
The response echoes `request_id` and includes:

- the control job ID, terminal state, completion time, invocation flag, and
  Feedback/outbox counts;
- the one persisted retrieval-run ID, retrieval status, and job-wide evidence
  and Reference counts;
- explicit group/document/source scope and a source chunk only for a retained
  `legacy_chunk` run;
- Feedback in exact durable finding-ordinal order;
- `digest=null` for zero findings, otherwise the authorized in-app digest
  identity, state, count, and immutable manifest hash; and
- safe query, corpus, index, embedding, retrieval-engine, and generation
  provenance.

For zero findings, generation was invoked successfully, `feedback=[]`, both
Feedback and notification-outbox counts are zero, and `digest=null`. This is a
successful preview, not an absent result. For positive findings, 1–4 exact
Feedback strings are returned and the one digest manifest must match their
identities and order. Reading a suppressed digest neither changes its state nor
counts as delivery.

## Authorization and failure privacy

Every read revalidates the authenticated submitter, explicit group membership,
source-document access, active changed-repository registration and approval,
linked retrieval run, evidence–Reference manifest, Feedback fingerprints, and
digest recipient/manifest. Deleted, unauthorized, cross-group, mismatched, or
contradictory durable records all return the same `preview_not_found` response.

The response never includes raw query text, source/evidence/renderer content,
repository paths, encryption metadata, idempotency material, lease tokens,
provider prompts or bodies, credentials, or internal endpoint URLs. Preview
performs no retrieval, generation, outbox insertion, dispatch, or delivery.

## CLI classification

`compair baseline preview` requires explicit `--group` and exactly one of
`--job-id` or `--digest-id`. It accepts no raw-query, evidence, renderer, or
prompt input. Success—including zero findings and a suppressed digest—is exit
0; usage is 2, authentication/authorization is 3, not-found is 4, and
transport/server/contract failure is 5. Stdout contains exactly one JSON value.

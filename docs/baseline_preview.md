# Baseline result preview

`compair baseline preview` is an opt-in, read-only view of an already durable
baseline generation result. It does not scan repositories, ingest a corpus,
build an index, start retrieval or generation, or deliver a notification.

Use an explicit group and exactly one durable selector:

```sh
compair baseline preview --group grp_example --job-id 00000000-0000-0000-0000-000000000001
compair baseline preview --group grp_example --digest-id 00000000-0000-0000-0000-000000000002
```

Group names use the same authenticated resolution as other CLI commands, but
an active or automatically selected group is intentionally not accepted. The
command accepts no retrieval query or repository content.

The command sends an authenticated strict JSON POST body. IDs are never placed
in the URL query string. The old `--run-id` selector is rejected because it
ambiguously referred to a retrieval run rather than the authoritative control
job.

On success, including zero findings or an authorized suppressed digest, stdout
contains exactly one `baseline-preview.v1` JSON value. Finding ordinal and the
durable control-job and digest states are preserved. Diagnostics are written
only to stderr. The command accepts no raw query, evidence, renderer, or prompt
flags.

Process exit codes are:

- `0`: authorized preview returned, including `suppressed`;
- `2`: command arguments are invalid;
- `3`: authentication or authorization failed;
- `4`: the authorized resource or explicit group was not found;
- `5`: transport, server, or response-contract failure.

Existing commands retain their prior generic failure code and behavior.

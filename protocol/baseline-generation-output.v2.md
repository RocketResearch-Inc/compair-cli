# Baseline generation output protocol v2

`baseline-generation-output.v2` is the strict structured response contract for
document-level baseline generation. It is independent of
`baseline-control-plane.v2`: the control plane reports durable job effects,
while this contract describes only the provider output that may later be
validated before Feedback persistence.

The complete JSON value is one object:

```json
{
  "schema_version": "baseline-generation-output.v2",
  "outcome": "no_findings",
  "findings": []
}
```

or:

```json
{
  "schema_version": "baseline-generation-output.v2",
  "outcome": "findings",
  "findings": [
    {"feedback": "A concrete, nonblank finding."}
  ]
}
```

## Invariants

- The input is UTF-8 JSON and contains exactly one JSON value.
- Duplicate object keys, non-finite numbers, and additional properties are
  invalid before semantic processing.
- `schema_version` is exactly `baseline-generation-output.v2`.
- `outcome=no_findings` requires an empty `findings` array.
- `outcome=findings` requires one through four findings.
- Every finding contains exactly one `feedback` string with at least one
  non-whitespace character.
- The anchored feedback pattern is
  `^(.|\n|\r|\u2028|\u2029)*[^\t-\r\u001c-\u0020\u0085\u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000](.|\n|\r|\u2028|\u2029)*$`.
  Its explicit whitespace class is equivalent to Core's `\s`/`str.strip()`
  semantics, while the surrounding alternatives preserve multiline content.
  This anchored form is portable to the supported Ollama structured-output
  converter; it does not weaken validation to `minLength` alone.
- Array order is the durable finding order and must be preserved unchanged.
- Plain text, blank output, the sentinel `NONE`, a JSON string containing
  `NONE`, malformed JSON, mismatched outcome/count combinations, empty or
  whitespace-only Feedback, and extra properties are invalid.
- A valid `no_findings` result creates no placeholder, empty, synthetic, or
  `NONE` Feedback row and creates no notification digest.

This freeze defines artifacts and validation only. It does not change the
production provider parser or enable generation execution.

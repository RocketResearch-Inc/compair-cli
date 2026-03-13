# Release Notes Template

Use this as a starting point when drafting a public Compair CLI release note.

## Compair CLI vNext Highlights

- **Single-user auto-login** - the client now inspects `/capabilities` and skips credential prompts when `auth.required` is false, storing the issued session token automatically.
- **Feature flag awareness** - capability payloads now include legacy-route and OCR availability; login surfaces helpful warnings when OCR uploads are disabled.
- **Updated core compose stack** - the sample Docker Compose file spins up the OCR sidecar and wires `COMPAIR_OCR_ENDPOINT` by default.

## Release Checklist

- [ ] Build CLI for all targets
- [ ] Build and validate any bundled Core helper images you intend to reference
- [ ] Run integration tests
- [ ] Tag and publish

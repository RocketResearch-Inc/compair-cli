# Local baseline repository registration and scan plans

This opt-in self-host workflow turns ordinary local Git repositories into the
existing `baseline-scanner-inputs.v1` plan without database edits, manual HTTP,
or copied log IDs. It does not start a scan, upload, index build, or run.

Repository authority comes only from an authenticated current group
administrator approving a `repository-identity.v1` descriptor whose authority
is `compair-local-repository.v1`. The CLI generates its random repository UID;
Core returns the authoritative opaque group-scoped registration ID. A path,
name, remote, revision, root commit, or filesystem attribute never authorizes a
repository.

First establish the changed repository's existing Core document:

```sh
compair track --group <group-id> /path/to/changed
compair --group <group-id> docs list --own-only --all-pages --json
```

Then register the changed and sibling repositories:

```sh
compair baseline repository register \
  --group <group-id> --path /path/to/changed \
  --source-document-id <document-id> --name changed \
  --allow-loopback-http --json

compair baseline repository register \
  --group <group-id> --path /path/to/sibling --name sibling \
  --allow-loopback-http --json

compair baseline repository list \
  --group <group-id> --allow-loopback-http --json
```

Remote HTTPS is required. `--allow-loopback-http` permits plaintext HTTP only
when the actual connected peer is loopback. Repository operations send no
local path or remote URL to Core.

Create and consume the plan:

```sh
compair baseline plan create \
  --group <group-id> --changed /path/to/changed \
  --base <base-ref> --head <head-ref> \
  --sibling /path/to/sibling \
  --output /private/path/plan.json \
  --allow-loopback-http --json

compair baseline scan \
  --group <group-id> --plan /private/path/plan.json --dry-run
```

Plan creation resolves immutable commits, rechecks every registration and
source-document association, and writes a private file atomically. It never
scans or uploads. Use `--overwrite` only to explicitly replace an existing
regular output file.

Local bindings are stored as versioned JSON files under
`~/.compair/state/baseline-repositories/`. Each contains the group and
registration scope, random UID, canonical local path, descriptor/path/Git
sanity hashes, and optional source-document ID. Files are HMAC-authenticated by
the existing `~/.compair/state/baseline-upload-install-secret.v1`, use private
permissions, and reject symlinks. They contain no credentials, authenticated
remote, file content, diff, query, or evidence.

Moving, recloning, or adding another working copy requires an explicit bind:

```sh
compair baseline repository bind \
  --group <group-id> --registration-id <registration-id> \
  --path /new/working-copy --allow-loopback-http --json
```

`bind` reauthorizes and requires an active registration; it never creates or
reactivates one. Disable and reactivate are group-admin-only:

```sh
compair baseline repository state \
  --group <group-id> --registration-id <registration-id> \
  --active=false --allow-loopback-http --json
```

Disabling immediately blocks new plans and submissions while preserving audit
history and the protected local binding.

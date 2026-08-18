# Config Reference

## Application-state root

Global CLI state defaults to `~/.compair/`. For clean isolation, set one
absolute private application root before invoking the CLI:

```bash
export COMPAIR_APP_DIR="$(mktemp -d)/compair-state"
```

Precedence is exact:

1. an explicit programmatic state directory supplied by an embedding caller;
2. `COMPAIR_APP_DIR` for installed CLI commands;
3. the legacy `~/.compair` default.

An invalid higher-precedence value fails closed and never falls through. On
macOS and Linux, a missing configured root is created as mode `0700`. An
existing root must be an absolute canonical directory owned by the current
user, must not be a symlink, and must have no group/world permissions. Empty,
whitespace-only, relative, inaccessible, non-directory, symlinked, and
permission-unsafe roots are rejected without printing the path. The configured
override currently fails closed on Windows because equivalent ACL validation
is not implemented; leaving it unset preserves the existing Windows default.

The application root contains:

```
credentials.json   # { auth_token } for email/password; future: { access_token, refresh_token, expires_at, user_id, username }
config.yaml        # { api_base, defaults: { notify, poll_interval, ... } }
profiles.yaml      # named API profiles + snapshot defaults
core_runtime.yaml  # managed local compair_core container settings
workspace.db       # sqlite index for tracked items and aliases
active_group       # current group id or name
cache/             # capability cache
state/             # baseline binding, installation-secret, and resume state
```

Telemetry settings are stored in `<application-root>/config.yaml` under:

```yaml
telemetry:
  enabled: true
  install_id: 0123456789abcdef...
  last_heartbeat_at: "2026-03-18T16:45:00Z"
```

## Project file: `.compair/config.yaml`
```yaml
version: 1
project_name: my-repo
group:
  id: grp_123
  name: Group
repos:
  - provider: github
    remote_url: git@github.com:org/repo.git
    repo_id: ""                  # currently unused
    default_branch: main
    last_synced_commit: a1b2c3   # updated by CLI
    document_id: doc_abc123      # returned by /create_doc
    unpublished: false           # when true, keep this repo out of cross-repo reference search
    pending_task_id: task_123    # saved when a long-running sync is still processing server-side
    pending_task_commit: a1b2c3
    pending_task_initial_feedback: 4
    pending_task_started_at: "2026-03-09T12:34:56Z"
```

The project file, `.compairignore`, checkout-local feedback cache, and generated
review reports remain repository-local. `COMPAIR_APP_DIR` deliberately does
not relocate them.

## Repo-local snapshot ignore file: `.compairignore`

Optional file in the repo root:

```text
# one glob per line
package-lock.json
scripts/*
docs/third-party-notices*
generated-sdk/docs/
```

Notes:

- Used by snapshot-based commands such as `review`, `sync`, `push`, `stats`, `diff`, and `snapshot preview`
- Best for large generated or low-signal tracked files that would otherwise crowd out more useful product-surface evidence
- Matching is intentionally simple: blank lines and `#` comments are ignored, and each pattern is matched against both the repo-relative path and the basename
- A trailing slash is a directory-prefix rule, so `generated-sdk/docs/` ignores every tracked file under that repo-relative directory
- Prefer straightforward patterns like `package-lock.json`, `scripts/*`, `generated-sdk/docs/`, or `docs/third-party-notices*` over full `.gitignore`-style syntax
- Run `compair ignore suggest` to print conservative candidates, or `compair ignore suggest --write` to append high-confidence suggestions

## Credentials
`<application-root>/credentials.json`
```json
{
  "auth_token": "...",
  "user_id": "usr_123",
  "username": "you@example.com"
}
```

## Profiles
`<application-root>/profiles.yaml`
```yaml
default: cloud
profiles:
  cloud:
    api_base: https://app.compair.sh/api
    snapshot:
      max_tree_entries: 0     # 0 means no cap
      max_files: 0
      max_total_bytes: 0
      max_file_bytes: 0
      max_file_read: 0
      include_globs: []
      exclude_globs: []
  local:
    api_base: http://localhost:4000
```

The `local` profile starts at `http://localhost:4000` for overlay/dev use. Running `compair core up` rewrites it to the configured localhost port for the managed Core container, which defaults to `http://localhost:8000`.

## Local Core runtime
`<application-root>/core_runtime.yaml`
```yaml
image: compairsteven/compair-core
container_name: compair-core
data_volume: compair-core-data
port: 8000
auth_mode: single-user         # or accounts
generation_provider: local     # local, openai, http, fallback
embedding_provider: local      # local or openai
openai_api_key: ""             # optional; 0600 file permissions
openai_model: gpt-5.4-mini
openai_code_model: ""          # optional; defaults to openai_model
openai_notif_model: ""         # optional; defaults to backend scorer default
openai_embed_model: text-embedding-3-small
openai_base_url: ""            # optional; for OpenAI-compatible endpoints
generation_endpoint: ""        # required only when generation_provider=http
```

`openai_model` now defaults to `gpt-5.4-mini`. Use `gpt-5.4` when you want the quality-first self-hosted path instead of the lower-cost default.

## Environment variables
- `COMPAIR_APP_DIR` – absolute private CLI application-state root; invalid configured values fail closed without falling back to `~/.compair`
- `COMPAIR_API_BASE` – override API base (`--api-base` flag wins)
- `COMPAIR_PROFILE` – select a profile by name
- `COMPAIR_ACTIVE_GROUP` – chosen group (falls back to `<application-root>/active_group`)
- `COMPAIR_TELEMETRY_BASE` – override the anonymous CLI telemetry collection base URL
- `COMPAIR_OUTPUT` – `text` or `json`
- `COMPAIR_DEBUG_HTTP` – set to `1` to log HTTP requests
- `COMPAIR_VERBOSE` – set to `1` to enable verbose output
- `COMPAIR_OPENAI_API_KEY` / `OPENAI_API_KEY` – fallback key source for `compair core` when you do not save the key into `core_runtime.yaml`
- `COMPAIR_OPENAI_BASE_URL` / `OPENAI_BASE_URL` – optional OpenAI-compatible base URL for local Core
- Hook environment (watch):
  - `COMPAIR_COMMITS`, `COMPAIR_FEEDBACK_COUNT`, `COMPAIR_SYNC_JSON`

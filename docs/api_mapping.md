# API Mapping (CLI ↔ API)

| CLI command | HTTP call(s) |
|---|---|
| `login --email --password` | `POST /login` (JSON `{username,password}`) → stores token and sends `Authorization: Bearer` (+ `auth-token` for compatibility) |
| `login browser` | `POST /auth/google/device/start` → opens browser to returned `auth_url` → polls `GET /auth/google/device/poll?poll_token=...` until completion, then stores the returned auth token |
| `login --token <token>` | No HTTP call; stores provided token in `~/.compair/credentials.json` for subsequent API requests |
| `signup --email --name` | `POST /sign-up` (JSON `{username,name,password}`) |
| `group create <name>` | `POST /create_group` (multipart/form-data) |
| `group ls` | `GET /load_groups?user_id=...&own_groups_only=true` |
| `group list-users [group]` | `GET /load_group_users?group_id=...` |
| `group join <group>` | `POST /join_group` (form) |
| `track [PATH] [--group <group>]` | `POST /create_doc` (form) → saves `document_id` for the repo document |
| `sync` | `POST /process_doc` (form: `doc_id`, `doc_text`, `generate_feedback=true`, optional `chunk_mode=client`) → `GET /status/{task_id}` |
| `watch` | Repeats `sync` on an interval; sends notifications and runs `--on-change` command |
| `docs list` | `GET /load_documents` (filters: `group_id`, `filter_type`, `own_documents_only`) |
| `activity` | `GET /get_activity_feed` |
| `notifications` | `GET /notification_events` |
| `notifications ack <id>` | `POST /notification_events/{event_id}/acknowledge` |
| `notifications dismiss <id>` | `POST /notification_events/{event_id}/dismiss` |
| `notifications share <id>` | `POST /notification_events/{event_id}/share` |
| `feedback rate <id>` | `POST /feedback/{feedback_id}/rate` (JSON `{user_feedback}`) |
| `feedback hide <id>` | `POST /feedback/{feedback_id}/hide` (form: `is_hidden=true|false`) |
| `notes add <doc_id>` | `POST /documents/{document_id}/notes` |
| `notes list <doc_id>` | `GET /documents/{document_id}/notes` |
| `notes get <note_id>` | `GET /notes/{note_id}` |

**Notes**
- Requests include `Authorization: Bearer` and `auth-token` (for SaaS compatibility)
- Most create/update endpoints are `form` or `multipart`
- `status` returns `PENDING|SUCCESS|FAILED` and may include a result payload

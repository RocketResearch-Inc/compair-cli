# baseline-repository-discovery.v1

This contract adds authenticated discovery for the existing durable baseline
repository registrations. It does not grant repository authority: authority is
created only by the existing group-administrator registration operation.

All operations are `POST` requests with UTF-8 `application/json` bodies. Core
applies the baseline control-plane byte limit, strict duplicate-key and
non-finite-number rejection, authenticated transport policy, and no-store
response headers.

## Requests

- `repository_list_request` contains exactly `schema_version`, `message_type`,
  `request_id`, and `group_id`. It is accepted only for a current administrator
  of that group at
  `/baseline/control/admin/v1/repositories/list`.
- `repository_inspect_request` adds exactly one opaque `registration_id`. It is
  accepted for a current authorized group member at
  `/baseline/control/v1/repositories/inspect`.

## Responses

`repository_list` returns `repositories` in ascending `registration_id` order.
`repository_inspection` returns one `repository`. Each safe repository value
contains only:

- opaque `registration_id` and `group_id`;
- immutable `identity_descriptor` and `identity_descriptor_hash`;
- `state`, either `active` or `disabled`;
- nullable authoritative `source_document_id`; and
- creation/update timestamps.

The descriptor UID is a stable label used for an explicit authenticated local
bind; it is not proof of ownership and never authorizes a request by itself.
The response never contains a local path, display name, remote URL, revision,
credentials, private idempotency value,
audit-user identity, lease, diff, query, content, or evidence. Unknown,
cross-group, deleted, and unauthorized inspection all use the existing generic
not-found response.

The local self-host authority is the existing `repository-identity.v1`
descriptor with `authority` exactly `compair-local-repository.v1` and a random
CLI-generated `repository_uid`. Clients must compare the returned descriptor
and hash with their protected local binding.

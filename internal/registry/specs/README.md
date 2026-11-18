# OpenAPI specifications

Place OpenAPI 3.x JSON files in this directory and rebuild to register service
commands. Each filename selects the service name unless the spec declares
`x-octo-service`.

`server.json` is derived from the [Octo Server v2 OpenAPI on the test branch](https://codex.mlamp.cn/dmwork/octo-server/-/blob/6ea222952bf1b8798f5e8c7bd10409af751fe8a9/docs/openapi/v2.yaml).
The CLI paths include the gateway's `/api` mount. The spec retains the
upstream operation IDs, parameters and schemas. CLI metadata selects
Token-derived Space, response envelopes, multipart uploads, and download
redirect handling. Mutations and credential issuance disable automatic
retries when a request outcome is uncertain. The spec marks storage
credentials and signed URLs for diagnostic redaction.


`loop.json` is derived from Fleet's `openapi/public-v2.json` on the
`fleet-cli-v2` branch at commit `cf6e3c068b88a801b213fab2810bdf19a929cd36`.
It preserves operation IDs, bodies, responses and action
metadata. CLI adaptation expands `/v2` plus `/cli` into the gateway mount
`/fleet/api/v2/cli`, selects Token-derived Space and data envelopes, requires
`--workspace-id` on Workspace-scoped operations, and keeps Workspace URL IDs
positional. Writes disable automatic replay; file transport and secret metadata
use the shared command engine. Synchronize this contract from Fleet when v2
changes; operations absent from
Fleet's CLI route manifest must not be restored by client-only edits.
The selected surface has 126 operations. Runtime and Workspace access is
read-only; additional task queries/watches, task views and Skill deletion-impact
preview are excluded.

The public service name is `loop`; Fleet remains the backend name and gateway
mount. A leading `loop.` in an operation ID is the service prefix, so skill
commands share `loop skill` rather than creating a nested `loop loop`.

The Loop compatibility layer retains the installed CLI's project/resource aliases,
task assignment/name selectors, legacy member flags and member pagination aliases.
Its actor-list requests resolve paths from the current registry. Fleet business
parameters remain declared in the upstream v2 document, rather than being added
only to this copy.

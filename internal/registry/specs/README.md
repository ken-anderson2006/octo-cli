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

The six `sidebar.*` operations are an additive OCTO-457 contract from the
[sidebar adapter in Octo Server](https://codex.mlamp.cn/dmwork/octo-core/octo-server/-/blob/codex/octo-457-sidebar-api/docs/openapi/v2.yaml).
They map to `server sidebar list`, `follow`, `mute`, `move`, `mark-read`, and
`category delete`. Their schemas mirror the existing native sidebar payloads;
their v2 authentication/actions and error envelope belong to the CLI adapter.
The CLI mirror expands the acyclic sidebar read-response references in its
envelope component so offline `schema sidebar.list` includes complete parent,
child and preview fields within the framework's existing reference budget.
The old Server operations above are retained unchanged. Deploy the adapter and
provision `im.sidebar:read` / `im.sidebar:write` in the authorization catalog
and target Token grants before calling them. CLI registration alone does not
make a missing backend route or permission available.

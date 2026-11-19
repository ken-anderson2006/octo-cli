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
[sidebar adapter in Octo Server](https://codex.mlamp.cn/dmwork/octo-core/octo-server/-/blob/a1d9d21196cbf70e55fdf58e5b01f67a3295446c/docs/openapi/v2.yaml).
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

`loop.json` is derived from Fleet's `openapi/public-v2.json` on the
`v2.5.0` tag at commit `7e63641e0a19c700557f64c6201021442b709502`.
It preserves operation IDs, bodies, responses and action
metadata. CLI adaptation expands `/v2` plus `/cli` into the gateway mount
`/fleet/api/v2/cli`, selects Token-derived Space and data envelopes, requires
`--workspace-id` on header-scoped operations, and keeps Workspace URL IDs
positional with the declared flag alternative. URL-scoped operations omit the
redundant Workspace header. Writes disable automatic replay; file transport and secret metadata
use the shared command engine. Synchronize this contract from Fleet when v2
changes; operations absent from
Fleet's CLI route manifest must not be restored by client-only edits.
The selected surface has 126 operations. Runtime and Workspace access is
read-only; additional task queries/watches, task views and Skill deletion-impact
preview are excluded.

The public service name is `loop`; Fleet remains the backend name and gateway
mount. A leading `loop.` in an operation ID is the service prefix, so skill
commands share `loop skill` rather than creating a nested `loop loop`.

Loop commands use only the shared engine's declared inputs. The contract exposes
`--project-id`, explicit assignee type/ID fields and member `--page/--page-size`.
It omits the member `limit`/`offset` convenience parameters and uses `--data` for
object values and nulls. Existing `x-octo-body-variants` metadata expresses the
mutually exclusive project lead fields. There are no Loop-specific aliases,
request conversions or implicit roster lookups in the command runner.

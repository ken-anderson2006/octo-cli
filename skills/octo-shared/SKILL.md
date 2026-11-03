---
name: octo-shared
description: Inspect the Octo CLI framework, select credentials, preview HTTP requests, and consume structured results.
---

# octo-shared

Use `octo-cli schema --list` to discover embedded operations. Inspect an operation
with `octo-cli schema <operation-id>`. Octo Server v2 commands are grouped under
`octo-cli server`. Use `server identity get` to read the verified Token owner,
bound Space and optional User Bot binding.

Use `octo-cli auth list` to see saved profiles and the default. Select a profile
with `--profile` or `OCTO_PROFILE`. For an external Bearer, use
`--credential-file`, `OCTO_TOKEN`, or `OCTO_TOKEN_FILE`. Set only one Token
environment variable. Without an explicit profile, external credentials use
`OCTO_API_BASE_URL` and the runtime Space. Use `octo-cli auth login` to import a
Token locally and `octo-cli auth logout` to remove a profile. Use
`octo-cli config show` for the resolved target and credential source.

Use `octo-cli api <METHOD> <PATH>` for a direct request to the configured
gateway. `--params` accepts a JSON object and `--data` accepts inline JSON,
`@file`, or `@-` for stdin. Preview the request with `--dry-run` before sending it.

Success uses stdout and defaults to a JSON envelope. Errors use stderr and
a nonzero exit status. Use `--jq` to select fields from the success envelope.

## Stored-secret download links over MCP

For a stored alias or ID, use `get_secret_download_url` and follow
[the private client-file workflow](secret-download.md). Download directly to
an exclusive private file; never fetch it as text, open/read/cat it into model
context, or echo it from scripts. The temporary URL is sensitive too. This
feature requires private shared Redis and an explicitly routed public HTTPS
MCP download service, including for stdio publishers. Rotation does not revoke
issued snapshots. The linked reference covers configuration, bounds, retention
and client-history limits.

## 10. MCP context admission

MCP verifies a `bf_` User Bot for each tool RPC, including discovery and direct
secret access. HTTP protocol requests also require a bearer. Production reads
the current per-Bot context policy through the opt-in verify-bot response;
missing or failed policy reads refuse the call. Other credential types are
outside this MCP contract. Every admitted tool request is durably recorded
before business execution using a closed argument whitelist; responses and
outcomes are excluded.

Strict mode requires a complete `params._meta.octo` v1 declaration containing
raw `sender`, `channel`, `channel_type` (1/2/5), `space_id`, and a lossless decimal
string `message_id` under `im`. Compatible mode permits complete absence, while
malformed declarations fail in either mode. This is a client source claim,
not membership evidence or authority to select the actor or destination.
OBO configuration and delegation controls are forbidden in MCP.

Operators must pre-create private POSIX request storage and set
`OCTO_MCP_REQUEST_LOG_DIR`. Debug requires explicit `--policy-debug` and
`--policy-debug-file` startup flags plus a private, origin-bound policy file;
it replaces only the policy source and still requires real Bot verification.
The debug snapshot changes only after restart. No automatic production
fallback or default-switch timer exists.

`mcp serve --dry-run` is rejected without networking. A running server's
`execute.dry_run=true` still authenticates, resolves policy, and persists the
request before previewing the business request. See
`docs/mcp-context-admission.md` for the exact wire and storage contract. IM
client injection, real group integration and management UI are separate work.

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

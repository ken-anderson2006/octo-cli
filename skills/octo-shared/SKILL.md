---
name: octo-shared
description: Inspect the Octo CLI framework, select credentials, preview HTTP requests, and consume structured results.
---

# octo-shared

Use `octo-cli schema --list` to discover embedded operations. Inspect an operation
with `octo-cli schema <operation-id>`. Octo Server v2 commands are grouped under
`octo-cli server`. Use `server identity get` to read the verified Token owner,
bound Space and optional User Bot binding.

Personal conversation operations use `octo-cli server sidebar`: `list`, `follow`,
`mute`, `move`, `mark-read`, and `category delete`. They require the OCTO-457
Server CLI adapter and `im.sidebar:read` / `im.sidebar:write` Token permissions.
They act as the Token owner in its bound Space, including with a Bot binding;
`--space` cannot select another Space. Existing group/thread/message commands
retain their own meanings.

`server sidebar list` is complete and unpaginated. Pair `--target-channel-id`
with `--channel-type` to read one DM or a complete group tree. Follow and mute
accept channel types 2 (group) and 5 (complete child ID); move also accepts 1
(DM). Explicit false uses `--is-followed=false` / `--mute=false`. To ungroup,
use `server sidebar move <channel-id> --channel-type 2 --data '{"category_id":null}'`;
omitting the category or passing an empty string does not ungroup.

`server sidebar mark-read <group-id> --channel-type 2 --scope group_and_threads`
clears the group and all accessible active/archived children. It is separate
from message receipts or single-chat read operations. Sidebar writes do not
automatically retry; `RESULT_UNKNOWN` needs reconciliation, and repeating a
group read clear can clear newer messages too.

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

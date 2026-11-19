# octo-cli

[![CI](https://github.com/Mininglamp-OSS/octo-cli/actions/workflows/ci.yml/badge.svg)](https://github.com/Mininglamp-OSS/octo-cli/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/Mininglamp-OSS/octo-cli.svg)](https://pkg.go.dev/github.com/Mininglamp-OSS/octo-cli)
[![License: Apache 2.0](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](./LICENSE)

`octo-cli` is an OpenAPI-driven command-line framework for HTTP APIs. It
builds a single Go binary with an extensible Cobra command tree, credential
profiles, request previews and structured output for scripts and agents.

This checkout embeds Octo Server v2 under `octo-cli server` and Fleet v2 under
`octo-cli loop`.
The framework commands are `api`, `schema`, `auth`, `config`, `skills`, `mcp`,
`upgrade`, `version`, `help` and `completion`.

## Build and try Octo Server v2

```sh
make build
./bin/octo-cli server --help
./bin/octo-cli schema --list server
```

Use a test-environment unified Token from the target Space. The test gateway
origin is `https://im-test.deepminer.com.cn` and the command paths include
`/api/v2/cli`. Server v2 derives the Space from the Bearer. The identity and
group list calls require `im.identity:read` and `im.groups:read`, respectively.
Set `token_file` to the path of your Token file.

```sh
export OCTO_API_BASE_URL=https://im-test.deepminer.com.cn
./bin/octo-cli --credential-file "$token_file" server identity get
./bin/octo-cli --credential-file "$token_file" api GET /api/v2/cli/identity
./bin/octo-cli --credential-file "$token_file" server group list --page-size 10
```

Use `--dry-run` to preview the request. Successful results carry the Server
payload in `data` and any pagination metadata in `_pagination`.
Use `server file upload --file <path>` for multipart uploads.
`server file download --path <object-key>` returns the storage URL in `data.url`.

### Personal conversation sidebar

OCTO-457 operations use `server sidebar` so existing group, thread and message
commands keep their current meanings. They require the sidebar CLI adapter in
Octo Server and a unified Token with `im.sidebar:read` for reads or
`im.sidebar:write` for mutations. The adapter operates on the Token owner in its
bound Space, including when the Token has a verified Bot binding. `--space`
does not change that scope.

```sh
octo-cli server sidebar list
octo-cli server sidebar list --target-channel-id "$group_id" --channel-type 2
octo-cli server sidebar follow "$group_id" --channel-type 2 --is-followed=false
octo-cli server sidebar mute "$group_id" --channel-type 2 --mute=true
octo-cli server sidebar move "$group_id" --channel-type 2 --category-id "$category_id"
octo-cli server sidebar move "$group_id" --channel-type 2 --data '{"category_id":null}'
octo-cli server sidebar mark-read "$group_id" --channel-type 2 --scope group_and_threads
octo-cli server sidebar category delete "$category_id"
```

The list returns the complete authorized active conversation trees without
pagination. Target reads pair `--target-channel-id` with `--channel-type` and
return one DM or the complete parent tree, without global categories/summary.
Channel types are `1` for a DM, `2` for a group and `5` for a complete child ID
(`group____child`). Follow/mute accept groups and children; move also accepts
DMs. `false` is an explicit value, and moving to ungrouped requires JSON `null`.

Group follow/mute transitions apply the backend's active-child cascade; repeated
parent requests preserve later child exceptions. Group mark-read includes all
accessible active and archived children. It does not replace single-chat/child
read or message receipt commands. Deleting a sidebar category preserves follows.
Mutations are sent once; an ambiguous transport failure is reported as
`RESULT_UNKNOWN` rather than automatically repeating an operation. Use `list`
to reconcile preferences before deciding whether to retry. Mark-read may have
cleared only some channels, so a retry can also clear newer messages.

`api <METHOD> <PATH>` accepts JSON query parameters through `--params` and a
JSON body through `--data`. Use `--data @file.json` or `--data @-` to read a
body from a file or stdin. Paths are relative to the configured gateway.

## Loop commands (Fleet v2)

Loop commands use the gateway's `/fleet/api/v2/cli` mount. Configure the gateway
origin and an owner-bound business Token using the credential options below.
The Token selects the Space. Workspace operations use an explicit UUID:

```sh
export OCTO_API_BASE_URL=http://localhost:28080
export OCTO_TOKEN_FILE=/absolute/path/to/fleet.token
./bin/octo-cli loop workspace list
./bin/octo-cli loop task list --workspace-id "$workspace_id" --page-size 10
./bin/octo-cli loop task create --workspace-id "$workspace_id" --title 'Example task'
./bin/octo-cli loop task get "$task_id" --workspace-id "$workspace_id"
./bin/octo-cli loop task update "$task_id" --workspace-id "$workspace_id" --status done
./bin/octo-cli loop task delete "$task_id" --workspace-id "$workspace_id"
```

Use `loop --help`, resource help, or `schema --list loop` to discover the
commands. Workspace-scoped commands require `--workspace-id`; Workspace detail
commands take their Workspace UUID as a positional argument. `--data` remains
available for complex bodies and explicit nulls. Task lists accept `--page` and
`--page-size`; pagination is returned in `_pagination`.

The embedded contract registers 126 Fleet operations, including tasks, comments,
experts, expert teams, runtimes, projects, labels, attachments and autopilots.
Writes do not automatically replay an uncertain request. File uploads use
`--file`; binary downloads support `--output`. Environment values and declared
signing secrets are masked in request previews and diagnostics.

Fleet remains a separate service. The local gateway must forward `/fleet/api/`
to Fleet; the local development environment can point that proxy at a native
`go run ./cmd/server` process. Fleet still validates Token actions, owner,
Project membership, resource ownership and lifecycle state.

### Declarative Loop inputs

The 126 operations are generated from `internal/registry/specs/loop.json` by the
shared command engine. Use the declared command and field names:

- Project resources use `project resource create` and `project resource delete`.
- Project selection uses `--project-id` within the selected Workspace.
- Assignment uses `task update` with `--assignee-type` and `--assignee-id`, or
  `--assignee-member-id` for an Octo Project member. Commands send those fields
  directly and do not scan member, expert or expert-team lists to infer a target.
- Workspace members use `--page` and `--page-size`. Their Workspace UUID is in
  the URL, supplied positionally or through `--workspace-id`.
- Object values and explicit nulls use `--data`. Search terms remain positional
  through the contract's `x-octo-positional` declaration.

For example:

```sh
./bin/octo-cli loop workspace member list --workspace-id "$workspace_id" --page 1 --page-size 20
./bin/octo-cli loop task update "$task_id" --workspace-id "$workspace_id" --assignee-type member --assignee-member-id "$member_id"
./bin/octo-cli loop task update "$task_id" --workspace-id "$workspace_id" --data '{"assignee_type":null,"assignee_id":null}'
./bin/octo-cli loop task update "$task_id" --workspace-id "$workspace_id" --data '{"project_id":null}'
./bin/octo-cli loop project resource create "$project_id" --workspace-id "$workspace_id" --data '{"resource_type":"github_repo","resource_ref":{"url":"https://github.com/example/repo"}}'
```

Former convenience inputs such as `task assign`, resource `add/remove`,
`--assignee`, `--project`, `--ref`, member `--limit/--offset`, and the old
`*-octo-uid` flag aliases are not registered. Use the declared ID fields and
JSON bodies above instead. Fleet continues to authorize the resulting requests.

## Configuration and credentials

For `api` and generated commands, the CLI reads the Bearer from
`--credential-file`, then `OCTO_TOKEN` or `OCTO_TOKEN_FILE`, then the selected
profile. Set only one Token environment variable. Profiles are selected by
`--profile`, `OCTO_PROFILE`, or the saved `current_profile`. An external
credential without an explicit profile uses `OCTO_API_BASE_URL` and the runtime
Space from `--space` or `OCTO_SPACE_ID`.

Use `octo-cli auth list` to see the saved profiles and default. Pass `--profile`
to choose a profile for one command, or export `OCTO_PROFILE` to choose one
for commands in the current shell. Edit `current_profile` in `config.json`
to change the saved default.

The CLI stores version 1 profiles in `~/.octo/cli/config.json`. Set
`OCTO_CONFIG_DIR` to use a dedicated private directory. A managed profile
binds the credential to its saved gateway origin and Space. Reimport to change
that binding.

Use `auth login` to save a Token locally. Choose one input, `--with-token` for
stdin or `--token-file` for a file. The business gateway validates the Token
when a command sends a request.

```sh
octo-cli --profile agent --space "$space_id" auth login \
  --api-base-url "$api_origin" --with-token < "$token_file"
```

A successful import selects the profile as `current_profile`. Managed credentials
use the system credential store when available or a private AES-256-GCM file
with a random master key. On macOS, managed directories and files use owner-only
permissions and accept empty or deny-only ACLs.

To add a profile that reads from a file you manage, add a `token_file` source
to `config.json`. Relative token paths are resolved from `OCTO_CONFIG_DIR` or
the default configuration directory.

```json
{
  "version": 1,
  "current_profile": "agent",
  "profiles": {
    "agent": {
      "api_base_url": "https://api.example.invalid",
      "space_id": "space-id",
      "token_source": { "type": "token_file", "path": "agent.token" }
    }
  }
}
```

Use `octo-cli --profile agent auth logout` to remove the profile and clean up
its unreferenced managed credential. A caller-owned token file stays in place.
`config show` displays the resolved target and credential source.

## Add an OpenAPI specification

Place an OpenAPI 3.x JSON file in [internal/registry/specs](internal/registry/specs)
and rebuild. The filename selects the service name unless the spec declares
`x-octo-service`. An operation such as `example.item.get` becomes
`octo-cli example item get`. Path parameters become positional arguments.
Query and header parameters and simple body properties become flags.

Use `schema --list` and `schema <operation-id>` to inspect the resulting
registry. See the [architecture guide](docs/architecture-design.md) for the
request flow and supported metadata. `octo-cli skills octo-shared` prints
the bundled framework guidance.

Successful commands default to a JSON envelope on stdout. Errors use a
structured envelope on stderr and a nonzero exit code. `--jq` filters the
success envelope and `--format` selects `json`, `table`, `csv`, or `ndjson`.
Use `--dry-run` to preview a request, `--verbose` for diagnostics, and
`--timeout` or `--no-retry` to control transport.

## Run as an MCP server

### Remote Streamable HTTP

Use the [CI deployment guide](ci/mcp/README.md) to publish the enterprise
service. For local testing, start a listener and point it at an Octo gateway.

```sh
OCTO_API_BASE_URL=https://octo-gateway.example.com \
  ./bin/octo-cli mcp serve --listen 127.0.0.1:8765
```

Route `/mcp` and `/v1/skills` to the same service through your HTTPS ingress.
Every `/mcp` request carries the caller's Octo Bearer. Business calls use
that token and the registered CLI service commands. Public `GET /v1/skills`
returns the enabled skills embedded in the running binary under `data.skills`.
[MCP integration guide](docs/mcp-integration.md)
and [Chinese guide](docs/mcp-integration.zh-CN.md).

### Local stdio

Start `octo-cli mcp serve` from an MCP client's stdio configuration using the
installed CLI binary:

```json
{
  "command": "/absolute/path/to/octo-cli",
  "args": ["mcp", "serve"],
  "env": {
    "OCTO_TOKEN_FILE": "/absolute/path/to/octo.token",
    "OCTO_API_BASE_URL": "https://api.example.invalid"
  }
}
```

`octo-cli mcp` displays help. In stdio mode, stdin and stdout carry MCP
messages. Closing stdin cancels active calls and shuts down the server.

The launcher can also provide `OCTO_TOKEN` or select a saved profile with
`["--profile", "agent", "mcp", "serve"]`. The normal CLI credential precedence
applies. The server can initialize and list tools before a credential is
available. Authenticated commands resolve their credentials on invocation.

`tools/list` exposes `skills` and `exec`. The read-only `skills` tool takes
`{}` and returns all enabled skill documents and their Markdown references.
The `exec` tool accepts `{"args":["schema","--list"]}`. Pass each
CLI argument as its own string. For example, to preview a POST request:

```json
{"args":["api","POST","/v1/items","--data","{\"name\":\"example\"}","--dry-run"]}
```

`exec` results contain `stdout`, `stderr` and `exitCode`. A nonzero exit code also
sets MCP's `isError`. Use inline JSON for request bodies and run local file
operations, credential management and upgrades directly in the CLI.

## Distribution

### npm, Bun or pnpm

For Node-based agent runtimes (OpenClaw, etc.):

```bash
npm install -g @mininglamp-oss/octo-cli

# Or use the global package manager for your runtime:
bun install -g @mininglamp-oss/octo-cli
pnpm add -g @mininglamp-oss/octo-cli
```

The npm package resolves the matching platform sub-package, which already
contains the prebuilt binary. Install does not download binaries from GitHub.

### Go install

```bash
go install github.com/Mininglamp-OSS/octo-cli/cmd/octo-cli@latest
```

### GitHub Releases

Download the latest binary for your platform from
[GitHub Releases](https://github.com/Mininglamp-OSS/octo-cli/releases):

```bash
# Archives are named octo-cli_<version>_<os>_<arch>.tar.gz for every platform,
# including Windows. Pick the one for your platform and substitute <version>
# (e.g. 0.5.0):
curl -LO https://github.com/Mininglamp-OSS/octo-cli/releases/download/v<version>/octo-cli_<version>_linux_amd64.tar.gz
tar xzf octo-cli_<version>_linux_amd64.tar.gz
sudo mv octo-cli /usr/local/bin/
```

Windows release archives are `.tar.gz` as well; Windows 10+ includes `tar.exe`.

### install.sh

```bash
curl -fsSL https://raw.githubusercontent.com/Mininglamp-OSS/octo-cli/main/install.sh | sh
```

### Upgrade a global package installation

```bash
octo-cli upgrade --dry-run
octo-cli upgrade
```

`upgrade` identifies the npm, Bun or pnpm installation that owns the invoked
command and updates it to the configured registry's stable `latest` release.
An equal or newer installed version is a successful no-op. It verifies the
installed package, platform binary and command before reporting success.

Updates enable optional dependencies for npm/pnpm and explicitly install Bun's
matching platform package, because its executable is required to run the CLI.

Registry mirrors, scoped registries and authentication use the owning manager's
native settings: npm's user/global `.npmrc` and environment, Bun's supported
`.npmrc` and global `.bunfig.toml`, or pnpm's native configuration. The invoking
project's registry settings do not redirect the upgrade.

Dry run checks local ownership without contacting a registry or installing.
Upgrade requires no Octo credentials and preserves `--format` / `--jq`.
Use the original installation method for raw binaries, `go install`, project-local
or temporary installs. Package-manager
failures stop the command; an installation failure can leave partially changed
files, so inspect the structured error before retrying.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for development and verification.

## License

[Apache 2.0](LICENSE).

## Optional COS distribution

See [Optional COS publishing](docs/cos-publishing.md) for local release tools.

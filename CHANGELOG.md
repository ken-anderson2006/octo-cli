# Changelog

## Unreleased — MCP secret download links

- Add `get_secret_download_url` once in each facade (4/3/6 tools total), keeping
  existing meta-tool contracts. Resolve stored aliases/IDs under the actual
  caller credential and publish a reusable 300-second snapshot capability
  through private shared Redis. TTL is operator-configurable; 600 seconds is
  guidance, not a hard cap. No raw resolver registry exposure is added.
- HTTP replicas serve exact attachment bytes; stdio publishes to the same
  explicitly configured HTTP service/Redis. Missing config fails closed for
  the feature. Public HTTPS routing, private Redis ACL/TLS and log/retention
  policy are deployment prerequisites, not deployed by this change.
- Ship direct-to-private-client-file instructions and the configuration,
  snapshot/expiry, bounded transfer and Redis persistence risk contract in
  `octo-shared/secret-download.md`.


- **MCP HTTP context is operator-controlled** — requests containing
  `X-Space-Id`, `X-Octo-Channel-Id`, `X-Octo-Channel-Type` or
  `X-Octo-On-Behalf-Of` now fail with HTTP 403 before dispatch, including
  empty, repeated and case-varied headers. They previously filled unset
  trusted context fields from client input. Use server `--space` / `--force-*`
  flags or their environment equivalents; proxy-injected context headers are
  no longer supported. Bearers remain isolated per request, and ordinary
  operation arguments remain subject to backend authorization.
- **Non-idempotent writes are no longer resent after an ambiguous failure** —
  the transport used to retry every request up to four times on a dropped
  connection, a timeout or a 429/502/503/504, so a `message send` whose response
  was lost could be delivered four times while the final error said not to
  retry. A POST or PATCH is now resent only when the dial failed; otherwise the
  CLI and MCP report `RESULT_UNKNOWN` (`result_unknown` from `execute`) after
  exactly one delivery, keeping the backend's message. A 429 is also
  `RESULT_UNKNOWN`, with its backend code in detail and Retry-After in the
  hint rather than resent, because a handler can answer 429 after it committed.
  Forwarding an `Idempotency-Key` no longer
  enables resends: octo-docs-backend honours that key for `html_ppt` only, so
  `docs create` / `docs comments add` / `docs versions` writes on any other
  document type were duplicated. Only an operation whose backend deduplicates
  every resend (`capability.install`) keeps the retries. net/http no longer
  resends a keyed write on its own after a reused keep-alive connection fails,
  and `--no-retry` now means a single delivery at that layer too.
- **A failed request is classified by its whole attempt history** — including
  the terminal attempt, so even a first-attempt 429 or 5xx after a committed
  mutation is `RESULT_UNKNOWN`, including with `--no-retry`. Once an
  attempt may have applied a write, later refused dials, a cancellation while
  waiting to resend, or a replay into 404 (a DELETE that committed and lost its
  response) end as `RESULT_UNKNOWN` instead of a definite failure; a keyed
  write that exhausts its retries is `RESULT_UNKNOWN` on the CLI, `call_op`
  and `execute` alike. A replay-safe write is no longer resent by net/http
  behind the retry loop, so a delivery on a reused connection followed by a
  refused dial stays `RESULT_UNKNOWN`, and `execute` reports `result_unknown`
  when the replay of such a write was refused with 400/401/409. A request whose every dial failed is reported as a plain
  `NETWORK_ERROR` known not to have been sent (`execution_error` from
  `execute`).
- **MCP `call_op` refuses unknown tool arguments** — `dry_run`,
  `schema_fingerprint` or any other key next to `operation_id` / `arguments`,
  and a repeated key, are refused with `-32602` before anything is sent.
  Previously they were dropped and the operation ran live.
- **Unicode spellings of the execute switches are refused inside operation
  arguments** — `ſchema_fingerprint` (U+017F long s) and other spellings that
  `strings.EqualFold` matches with `dry_run` / `schema_fingerprint` are now
  refused on `execute` and `call_op` like their ASCII spellings, instead of
  being forwarded to the backend.
- **MCP positional query arguments** — `task.search` from `execute`, its dry run
  and `call_op` failed locally with "a flag in the command line was not
  recognised": an argument whose query parameter declares `x-octo-positional`
  (`q`) was rendered as a `--q` flag the command never registers. It is now
  passed as the command's operand after `--`, in spec order, so a search term
  with spaces, Unicode or a leading `-` reaches the wire unchanged. The MCP
  argument name stays `q`; the CLI label `query` is still refused, with a hint
  naming `q`.

- **Case-fold spellings of declared secrets are masked** — `--dry-run`,
  `--verbose` and MCP `execute` dry runs now mask a declared `x-octo-secret`
  value whose property name differs from the declared one only by Unicode
  case folding (for example `HEADERS`, `URL` or `header\u017f` inside a
  `capability.install` MCP server object). The request still carries exactly
  what the caller wrote.
- **Bot API coverage for the MCP facades** — 84 operations added, plus the
  three drive share leaves MCP used to refuse, across messaging, bot, docs,
  HTML docs, drive and Loop, each verified against
  the pinned backend route, handler and validation code: incoming webhooks for
  groups and threads, single-message reads, card revision clearing, thread
  archive/delete, mention preference, target resolution, OBO grant and
  principal lookups; document invites, access requests, batch forward grants,
  comment tasks, recent views, attachment copy/ingest, file export and PPT
  media; HTML version source/revert/diff, comment reanchor/delete/react and
  rendered pages/assets; drive trash, share transfer/existing copies,
  ancestors, doc liveness and role-checked mounts; Loop workspace member add
  and capability install. The row-by-row scope, evidence and tests are in
  `docs/mcp-bot-api-coverage.json`. `thread delete` returns as a high-risk
  operation now that `thread archive` gives bots a way to close a thread;
  `thread archive` and `docs scene import` are also high-risk (a Bot cannot
  unarchive, and `mode=replace` overwrites the scene). Loop workspace and
  runtime management stays hidden from the CLI and MCP by owner decision.
  Group and thread webhook create/regenerate return the push token and URLs in
  the result, as accepted by the owner. Risk labels are descriptive, not an
  enforced confirmation step.
  `capability install` masks the expert environment maps and each MCP
  server's `env`, `headers` and `url` in `--dry-run` output and `--verbose`
  traces (and the MCP dry run); the request itself is unchanged. Secrets are
  found through a registry walk that follows references, dynamic maps and
  `oneOf` alternatives below the resolved request-body tree.
- **Raw request-body uploads** (`x-octo-raw-body`) — five docs routes that take
  the file bytes as the body (PPT media, SVG attachments, docx/markdown/xlsx
  import) are generated operations with `--file`, a declared media type and a
  local size limit. Over MCP they use the same confined upload descriptor as
  multipart uploads; a dry-run never opens the file.
- **Inline binary results over MCP** — a binary-response operation called
  through MCP returns its body inside the envelope (`content_text` for UTF-8
  text, otherwise `content_base64`, always with `sha256`) up to 4 MiB; larger
  bodies report `inline: false` with their size and sha256 and are streamed,
  never held whole in memory. `docs scene export --image-format excalidraw`
  returns the portable file inline the same way. Nothing is written to the
  server's filesystem and `--output` stays unavailable to MCP callers. This
  applies to every binary operation reachable over MCP, including the existing
  mail raw/attachment, attachment, file and plugin downloads.
- **MCP exposure table** (`internal/mcp/exposure.json`) — MCP discovery,
  describe and execute are default-deny: an enabled operation must be listed
  (pre-existing baseline or an approved inventory row) or it is withheld and
  refused, and CI fails until it is classified.
- **`octo-cli mcp serve --facade two|both` (two-tool Skill-driven facade)** — an
  opt-in alternative tool surface over the same MCP engine, folding the three
  meta-tools into two verbs. It is off by default (`--facade three`), so the
  existing three-tool behaviour is unchanged; `--facade both` exposes all five
  for controlled side-by-side comparison.
  - `get_skill` — a discriminated discovery tool. `intent=search` is the
    `search_ops` discovery backend (domain/keyword filter, or the service↔skill
    module map with neither); `intent=describe` returns one operation's full
    zero-drift schema plus its Skill reference, a schema fingerprint, a
    per-operation `constraint_enforcement` projection, and progressive `depth`
    (`summary` outline or `full` schema).
  - `execute` — invokes one operation through the identical assembly / identity
    / validation / transport / envelope / authz backend as `call_op`, wrapped in
    a structured status (`ok` / `validation_error` / `ambiguous` /
    `execution_error` / `schema_drift` / `result_unknown` / `auth_error`).
    `ambiguous` is returned only when `operation_id` does not resolve to an
    operation: nothing is sent, and the payload keeps the shared
    `UNKNOWN_OPERATION` error plus up to five `candidates` to choose from. It
    never describes the outcome of a sent request.
    `dry_run=true` constructs and locally validates the request without sending
    it (no server-side success implied); for multipart operations it validates
    required path/query/header/body fields and the file binding without opening,
    reading, or materializing the file, and never contacts the backend. An
    optional `schema_fingerprint` from `describe` makes `execute` refuse with
    `schema_drift` if the embedded schema changed since it was read. The
    fingerprint is deterministic schema-drift detection only — it does not prove
    `describe` was called this session; a stateful session challenge is deferred.
    Machine-readable outcome semantics distinguish `result_unknown` (a mutating
    request whose outcome is ambiguous after a transport/timeout/429/5xx — may have
    applied, do not auto-retry) and `auth_error` (fix the credential) from a
    plain `execution_error`. Error and degradation hints are facade-aware: under
    `--facade two` they route to `get_skill`, never the disabled
    `search_ops` / `describe_op`.
    `tools/call` params accept only `name`, `arguments` and `_meta`, each once
    and exactly spelled, so a `dry_run` or `schema_fingerprint` placed beside
    `arguments`, or a second `arguments` object, is refused instead of being
    dropped while the call runs live. Inside operation arguments, a misplaced
    `dry_run` / `schema_fingerprint` and any undeclared name that equals a
    declared one under Unicode case folding (for example `\u017fpace_id` for a
    connection-forced `space_id`) are refused for `execute` and `call_op`.
    These name checks run before the credential is resolved, so they are
    bounded: one call accepts at most 1024 top-level argument names, aliases
    are found through a case-fold index rather than by comparing every pair,
    and a request whose context is cancelled or times out during validation is
    refused as `REQUEST_CANCELLED` with nothing sent. The
    multipart / raw-body dry run resolves the credential and token-kind gate,
    refuses the empty or dot-segment values a real call refuses, and lists
    query, header and form arguments where they would be sent. `get_skill`
    arguments are a closed object.
  The facade reuses the existing registry/schema, Skill mapping/resources and
  over-privilege protection white-list unchanged; contract and side-by-side comparison
  tests assert both facades drive the backend to the same wire.

- **`octo-cli mcp serve` (MCP server, v2.3 initial)** — runs octo-cli as a Model
  Context Protocol server over stdio (default, local/trusted-client transport)
  or HTTP (`--http <addr>`): JSON-RPC request/response over POST — one request,
  one response; no SSE stream or server-managed session yet. It exposes three
  meta-tools rather than one per operation, so a client's `tools/list` stays a
  few hundred tokens instead of expanding all embedded operations into resident
  schemas:
  - `search_ops` — discover operations by domain/keyword, or a live
    service↔skill module map with no arguments; each hit carries Skill
    navigation metadata (which business Skill to load, a short summary, a
    recommendation level, and the resource URI), never the Skill body.
    Keyword search keeps the leading 8192 bytes (without splitting UTF-8),
    then 512 runes, then 16 whitespace-delimited terms before an AND scan.
    Excess input returns `truncated: true` with a bound notice; ordinary
    multi-term matching is unchanged. Cancellation/deadline expiry returns an
    MCP tool error (`isError: true`, `SEARCH_CANCELLED`) without partial hits.
    The scan checks cancellation between operations and terms on both transports.
  - `describe_op` — the full, zero-drift parameter schema for one operation
    (straight from the embedded spec) plus the operation's Skill reference and
    a short pre-call advice; the Skill never carries a second copy of the
    schema.
  - `call_op` — invokes one operation by driving the existing command engine
    in-process, so identity routing, request assembly, pre-flight validation,
    transport, secret masking, and the JSON envelope are reused unchanged.
  Skills are exposed for progressive on-demand reading through MCP resources
  (`octo://skills/<name>/<file>.md`); clients that do not negotiate resources
  degrade to a skill `name` + readable hint and the three tools still work
  standalone. The service→Skill map comes from each `SKILL.md`'s frontmatter
  `services:` line plus a new embedded `skills/manifest.json`, merged and
  validated fail-fast at start (unknown service/op, uncovered service, missing
  reference file or heading anchor, disabled-service drift, and length/level
  caps are build/CI errors). Security-sensitive arguments (space, session-bound
  channel, on-behalf-of) can be forced per connection so a model-supplied value
  is overridden; the white-list is per operation, so a same-named field such as
  drive's `space_id` is never affected. `--dry-run` turns the whole server into
  a rehearsal: every `call_op` prints the request it would send as a
  `"dry_run": true` envelope and performs no backend mutation for the server's
  lifetime. Over HTTP the credential is per
  connection (the server env credential is never inherited), multipart
  `file_path` uploads are confined to `OCTO_MCP_UPLOAD_ROOT`: component-by-component
  no-follow opens (Linux/macOS `openat`; Windows directory-relative `NtCreateFile`)
  reject symlinks/reparse points and the final descriptor must be a regular file.
  Multipart assembly reads that already-open descriptor, so replacing a pathname
  after validation cannot redirect the upload. The explicit stdio opt-in still
  permits unconfined regular files; HTTP always requires confinement. `Origin` is
  validated (allowlist via `OCTO_MCP_ALLOWED_ORIGINS`, loopback-only by
  default), and `--http` binds cleartext so TLS/loopback termination is the
  operator's responsibility. Existing CLI commands, authentication, output, and
  exit codes are unchanged — the MCP server is a new front end over the same
  engine.


All notable changes to `octo-cli` are documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Changed

- **API base URL precedence** — the active profile's stored `api_base_url` now
  outranks `OCTO_API_BASE_URL`, which outranks the production default. A stored
  profile is routed to the origin it was created against even when the
  environment names another one; environment credentials and profiles without a
  URL still use the environment value. To move a profile to another gateway, use
  `octo-cli auth update --api-base-url <origin>`; `--clear-api-base-url` removes
  the stored origin so the environment or default applies again and reports
  `api_base_url` as an empty string, keeping the field's string type. `config
  show` and `auth status` expose the winning layer as `api_base_url_source`;
  an invalid stored origin returns a structured validation error naming the
  full `--profile <name> auth update` recovery commands, and conflicting update
  actions are refused with the same taxonomy.
- The default build starts with an empty service registry.

### Fixed

- **Numeric text fields** — query flags and multipart scalar fields use plain
  decimal notation for large and small values while preserving fractions.
- **Output preflight** — invalid effective output formats and jq parse/compile
  errors are rejected before requests or local mutations. Compiled jq programs
  are reused for output; help-only service parents, help and shell completion
  retain their text output independently of output options. Jq parse and compile
  failures use static diagnostics without caller-provided identifiers.
- **Local mutation dry-runs** — `auth login/update/logout` and `skills --install`
  reject `--dry-run` before reading credentials or changing local files.
- **`auth status` credential selection** — reports the same credential source,
  masked token, and effective API origin as ordinary requests, including when
  environment credentials override stored profiles. Explicit selectors still
  fail closed; unverified environment Bot IDs remain `robot_id_claimed`.
- **COS publish lock diagnostics** — cleanup warnings include a safe HTTP
  status without exposing SDK messages, signed URLs, or credentials. Cleanup
  failures still preserve the original publication result.

### Removed

- Built-in service specifications, domain-specific workflows and command aliases.
- Service-specific credential, routing, validation, and output branches.
- Bundled domain skills and integration guides.

## [0.5.0] — 2026-05-28

### Added
- **`octo skills` command** for embedded skill discovery — lists or
  extracts the Agent skills bundled in the binary (`octo-shared`,
  `octo-messaging`, `octo-files`, `octo-matter`). Useful for agent
  runtimes that load skills from `octo` at startup. (#16)
- **Encrypted credential profiles** via `octo auth login | status |
  logout | list` — bot tokens now live in `~/.octo-cli` (plaintext
  `config.json` metadata + AES-256-GCM `credentials.enc` token store)
  keyed by `--profile` / `--bot-id` (env `OCTO_BOT_ID`). `OCTO_BOT_TOKEN`
  remains a fallback. Each success envelope echoes the active
  `identity` (`{profile, robot_id, bot_kind, source}`) so agents can
  detect credential misuse. (#17)

### Changed
- **Matter domain withheld** behind a new `x-octo-disabled` spec flag —
  the spec stays embedded (`octo schema matter.*` still introspects it)
  but the command subtree and the `octo-matter` skill are hidden until
  the backend Matter API stabilises. Flip the spec flag and the skill
  frontmatter to re-enable. (#19)
- **Service-domain parent commands** (`octo thread`, `octo group`,
  `octo file`, …) now reject unknown subcommands with
  `unknown subcommand %q for %q` and exit 2 instead of silently printing
  help and exiting 0. Required by the `thread delete` removal so
  automation can detect a missing operation; also catches typos like
  `octo group lisst`. Parent help (`octo thread`, `octo thread --help`)
  still works without a token; only leaf operations require
  authentication.

### Removed
- **`octo thread delete`** — withdrew the thread-deletion command.
  Threads expose no bot-accessible archive or soft-close path, so a
  hard delete was inconsistent with the convention that bots get no
  destructive operations. The backend route is untouched; only the CLI
  command and its `thread.delete` spec entry are removed.

### Fixed
- **`octo file presigned`** and other `bot_api` specs realigned with
  octo-server — `file.presigned` now declares the required `fileSize`
  query param (previous calls failed with `HTTP 400 fileSize 参数必填`).
  Several other spec drifts also corrected. (#14)
- **Auth gate on service parents** no longer fires for parent help or
  unknown-subcommand handling — the `unknown subcommand` envelope and
  `octo thread --help` now work without a bot token, restoring the
  safety property of the `thread delete` removal.

## [0.4.0] — 2026-05

### Architectural overhaul — metadata-driven command tree

This release replaces the hand-written command layer with an auto-generated
one, redesigns the output contract around a JSON envelope, and adopts
Factory-based DI throughout.

### Added
- **Metadata-driven service engine** (`cmd/service`) — every service command
  is auto-registered at startup from embedded OpenAPI 3.x specs. Adding or
  changing an endpoint is a spec edit, no Go code change.
- **Seven domain specs** embedded via `//go:embed`: `matter` (14),
  `message` (4), `group` (9), `thread` (9), `file` (4), `bot` (6),
  `event` (2) — 48 operations total.
- **JSON envelope I/O** (`internal/output`) — stable
  `{ok, identity, data, _pagination, _rate_limit}` success shape and
  `{ok:false, error:{type, code, message, hint, detail}}` error shape.
- **Error taxonomy + exit codes** — `auth_error` → 3, `validation`/`config`
  → 2, all others → 1.
- **Factory DI** (`internal/cmdutil.Factory`) — `ConfigFunc`,
  `CredentialFunc`, `ClientFunc`, `RegistryFunc` accessors; no mutable
  package-level globals.
- **Multi-service routing** — each operation selects its base URL from
  `x-octo-base-url` (matters, dmworkim, or the `OCTO_API_URL` fallback).
- **Universal flags** — `--format`, `--jq`, `--dry-run`, `--verbose`,
  `--timeout`, `--no-retry`, `--space`, plus `--page-all` / `--page-limit`
  on paginated operations.
- **Retry with exponential backoff + jitter** and `Retry-After` handling
  (client).
- **Multipart upload** and **binary/redirect response** handling for the
  `file.*` operations.
- **`octo schema` discovery command** — list and inspect the embedded spec
  registry fully offline.
- **`octo api` generic passthrough** — raw `METHOD /path` against any
  configured service.
- **`octo config show`** — resolved config with the bot token masked.
- **Agent skills** under `skills/` — `octo-shared`, `octo-matter`,
  `octo-messaging`, `octo-files` with YAML frontmatter.
- **Shell completion** via cobra's built-in `completion` command (bash,
  zsh, fish, powershell).
- **CI**: gofmt, vet, golangci-lint, `go test -race`, coverage, build,
  `go mod tidy` drift check, Go 1.24.x.

### Changed
- **Bot-only identity model.** `OCTO_BOT_TOKEN` carries an `app_*` (App
  Bot) or `bf_*` (User Bot) token; user login is removed.
- **Agent-first output.** Default format is `json`; no interactive prompts;
  stable machine-parseable envelope on every path.

### Removed
- Legacy hand-written `todo`, `goal`, `assign`, `comment`, and `attachment`
  subcommand trees. All equivalent functionality is now under `octo matter`
  and auto-registered from the spec.

[Unreleased]: https://github.com/Mininglamp-OSS/octo-cli/compare/v0.5.0...HEAD
[0.5.0]: https://github.com/Mininglamp-OSS/octo-cli/compare/555c959...v0.5.0
[0.4.0]: https://github.com/Mininglamp-OSS/octo-cli/tree/555c959

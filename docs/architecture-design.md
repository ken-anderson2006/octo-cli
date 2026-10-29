# Octo CLI framework architecture

`octo-cli` builds its service command tree from OpenAPI 3.x metadata at startup.
This build registers Octo Server v2 under `server` and Fleet v2 under `loop`.

```text
OpenAPI JSON -> Registry -> Cobra commands
                                 |
                        Factory and command runner
                                 |
                    HTTP client -> output formatter
```

| Component | Responsibility |
|---|---|
| `cmd/octo-cli` | Process entry point, signal handling, and exit status |
| `cmd` | Root flags, configuration, credential profiles, raw API calls, discovery, and distribution commands |
| `internal/registry` | Embedded specifications, operation discovery, and schema extraction |
| `cmd/service` | Command generation, argument validation, request building, and pagination |
| `internal/cmdutil` | Per-invocation configuration, credentials, transport, and output |
| `internal/client` | HTTP, authentication headers, retries, request previews, and diagnostics |
| `internal/output` | Response normalization, output formats, and error envelopes |

## Specification metadata

Store each specification as JSON in `internal/registry/specs`. Its
`operationId` supplies the command words. A specification owns each HTTP
method, path, and parameter schema. Generated requests use the configured
gateway origin and the path supplied by the specification.

Optional extensions configure shared engine features:

- `x-octo-service` and `x-octo-disabled` control service naming and discovery.
- `x-octo-cli-hidden` omits individual operations.
- `x-octo-flag` names a CLI flag. `x-octo-positional` can promote a required
  string query parameter to a positional argument.
- `x-octo-pagination` declares cursor and response field paths.
- `x-octo-space-header: false` omits the Space header for a service that
  resolves its Space from the Bearer.
- `x-octo-data-envelope: true` puts the payload of a backend data envelope
  directly in the CLI's `data` field.
- `x-octo-multipart` and `x-octo-binary-response` describe file transport.
- `x-octo-secret` supplies redaction metadata.
- `x-octo-response-fields`, `x-octo-lossless-id-fields`, and
  `x-octo-response-unwrap` configure output normalization.
- `x-octo-strict-request-schema` and `x-octo-strict-response-schema` enable
  additional contract checks.
- `x-octo-retry` and `x-octo-auto-idempotency-key` describe retry and
  request-key behavior.

The generated commands validate their arguments before sending requests.
Optional `--data` inputs merge with explicit body flags. The raw `api`
command accepts a method and path directly. Matching specifications supply
the Space header policy, retry behavior, redirect handling, response envelope
and diagnostic redaction.

## Verification

Framework tests use synthetic OpenAPI specs outside the production registry.
The HTTP tests use local servers. Run `make test` to execute the Go suite
with the race detector and `npm --prefix npm test` to verify the distribution
launcher and package-manager behavior.

## Retry behavior

GET, HEAD, OPTIONS, TRACE, PUT and DELETE are idempotent (RFC 9110 §9.2.2) and
retry on every retryable failure. A POST or PATCH may already have been applied
when the connection drops after the request was sent, the server answers 429 or
a 5xx, or the request times out, so it is not resent; the failure is reported
as `RESULT_UNKNOWN`, keeping the backend's message and payload, including its
original error code in detail. A terminal 429 retains its HTTP status and
Retry-After guidance alongside the instruction not to retry automatically.
This includes the first attempt and `--no-retry`; all terminal 5xx responses
likewise retain their possible delivery in the outcome. It is still resent when
the dial failed (nothing reached the server; reported as `NETWORK_ERROR`). The API
has no general idempotency guarantee for POST, so this applies to a POST that
only reads as well. Carrying an idempotency key is not enough to resend: a
backend may accept a key and ignore it for some resources (octo-docs-backend
reads `Idempotency-Key` on `html_ppt` paths only). Only an operation listed in
`verifiedReplayDedup` (`cmd/service/run.go`), whose backend deduplicates every
resend of the same key, keeps the idempotent retries; `x-octo-retry: never`
still disables retries entirely.

Every mutating request, including a replay-safe one, is kept from net/http's
own replay of a request on a failed reused keep-alive connection, which the
Transport applies to any request with an `Idempotency-Key` /
`X-Idempotency-Key` header and reports only as its last send: the key goes on
the wire under its lower-case spelling, so each attempt passes through the
retry loop and its history. With `--no-retry` a safe request is kept from that
replay as well. MCP `execute` reports an engine `RESULT_UNKNOWN` as
`result_unknown` whatever the Type of the last response.

The outcome is decided from the whole attempt history: once an attempt may
have taken effect (anything but a failed dial or a definite 4xx other than
429), a later refused dial, 4xx or cancellation does not turn a mutating
request into a definite failure; it is `RESULT_UNKNOWN`. Only a definitive
success ends that uncertainty.

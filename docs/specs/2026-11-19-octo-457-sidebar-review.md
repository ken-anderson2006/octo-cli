# OCTO-457 CLI review and verification

The final CLI candidate targets GitLab `test` at `6c4f235`. It was created from
main `ddd1a50`, whose tree contained only the initialization README, then merged
with the existing test implementation and its later Loop update. The imported
implementation is excluded from the authored diff against test. The backend
adapter targets test `6d265362`; the original sidebar MR was already merged.
No frontend, deployment, release, approval or merge was performed by this task.

## Three review/fix rounds

Two independent read-only reviewers covered the complete CLI candidate and its
backend contract/authority boundary. The author checked each confirmed finding
and retained failing probes separately from passing runs. Reviews are self-audit
assistance, not independent GitLab approval.

| Round | Finding and final correction | Verification |
| --- | --- | --- |
| 1 | OpenAPI 3.1 mute allowed the null type but omitted null from its enum. Both producer and CLI now enumerate 0, 1 and null. | A Go native response containing inherited mute:null failed JSON Schema validation before the correction and passed afterward. |
| 1 | Deep response references exceeded the CLI's three-reference budget, hiding base conversation fields and leaving child refs unusable offline. | The public schema command regression first failed on missing channel_id. The CLI-only read envelope is now expanded without remaining refs; parent, child and preview fields are present, with the same fully resolved producer contract. The global budget is unchanged. |
| 2 | New ID max lengths were advertised but not checked locally. | Primitive min/max and real oversized sidebar-command regressions failed before the correction. Opted-in strict string paths now check Unicode code-point length without echoing values. Existing Server operations have no strict opt-in; current Loop operations declare no path length bounds, so existing paths retain their behavior. |
| 3 | Integrating the latest backend test changed UserOnly to reject Bot-bound Tokens, breaking the intended personal-owner behavior. | The unchanged SDK/limiter regression reproduced owner vs bound-bot mismatch. The adapter now uses the target's AsTokenOwner mechanism before limiting and Space checks, preserving the verified binding in Principal. Full race and live tests then passed. |
| 3 | The updated API gate rejected an anonymous empty-object response schema; final source links also needed the separate adapter branch. | Both mirrors reference the existing canonical EmptyData for data:{}, source links identify the new adapter, and the complete API check passes. One introduced lint shadowing warning was renamed; no introduced lint issues remain. |

During initial implementation, a separate regression reproduced the existing
required-nullable validator rejecting a present category_id:null. Required now
checks presence and declared nullability; enum/composition/uint64 constraints
still run. Omission, non-nullable nulls, empty IDs and wrong body types remain
rejected. This was tested before its fix, including the primitive gate.

The final integration seal rechecked all six resolved method/path, operation ID,
parameter/body, action, principal and success-response contracts. Existing Server
paths/components and the newly merged Loop registry are structurally identical
to their target versions. Documentation preserves both services.

## Final author-executed checks

| Check | Result |
| --- | --- |
| CLI Go 1.25 full race/shuffle suite | 1,061 passing records including subtests / 426 top-level tests; zero failures or skipped tests. Eleven tested packages pass; two packages have no tests. |
| CLI vet and production build | Passed. |
| CLI lint for introduced changes | golangci-lint v2.12.2 with --new-from-rev=origin/test: 0 issues. |
| Full lint | Does not pass: 5 gocritic and 6 staticcheck diagnostics in unchanged target files. They are not counted as introduced or reported as a passing gate. |
| Backend sidebar and SDK router race suites | 337 passing sidebar records / 117 top-level tests and 26 passing router records / 12 top-level tests; zero failures/skips. |
| Backend full server build, affected vet, i18n | Passed. |
| Backend make openapi-check | Passed after target synchronization and EmptyData correction, including 11 bearer tests and 23 summary tests. Existing informational warnings remain. |
| Backend make openapi-diff | Target unavailable; attempted, not claimed passed. Structural preservation/resolved-contract comparison is separate evidence. |
| Generated and raw mutation transport | All five mutations send once and report RESULT_UNKNOWN on an ambiguous 502. They send the configured Bearer and omit caller-selected Space headers. |
| Actual MCP subprocess | list, move with spaced JSON null, offline schema, and non-replayed group clear pass. |
| Whitespace and source-secret checks | Passed before publication. |

Earlier failing runs are retained, including the old command absence, required
null, response enum/ref-depth, path length, and post-target-integration actor
mismatch. They are not added to passing test totals. A macOS race-linker warning
appeared on backend runs; the processes exited successfully.

## Full-process acceptance and limits

The final CLI binary and full backend were rebuilt after the latest target merge
and owner fix. A loopback gateway fixture routes the actual /api/v2/cli paths to
the full server. MySQL 8, Redis, WuKongIM, SQL/binlog state, session provisioning,
legacy/native HTTP comparison and CLI subprocesses are real owned local services.
The Auth SDK is real, but its upstream Auth Server is a controlled HTTP authority
fixture with explicit owner, Space, binding and action snapshots. It is not
production or deployed-Auth acceptance.

All **12 cases pass**, with no failures or skips: complete/bound-Space reads,
complete child-target tree, follow and mute false/repeat semantics, category move
and null ungroup, category delete preserving actual unread/follows, parent plus
active/archived IM read clears, local no-HTTP validation, read-only permission,
Space override refusal, owner-versus-Bot behavior and current membership revocation.
See [the case records](octo-457-sidebar-cases.csv).

The temporary authority/gateway fixtures were stopped and the owned QA backend
restored without that authority configuration. No real user credential was added
to source or passed in command arguments.

Before rollout, the companion Server adapter must be deployed, the existing CLI
SDK router configured, and im.sidebar:read / im.sidebar:write catalog entries and
target Token grants provisioned. Code constants do not create grants. Existing
CLI commands are never silently rerouted, and no legacy credential fallback is
introduced. Real Auth Server grants, deployed gateway, production load and
independent approval remain outside these author-run checks.

Sanitized evidence is retained locally under octo457-cli-*.log/jsonl,
octo457-cli-live, and the owned backend runner's cli-* / reviewer-* labels.

## MR review follow-up — 2026-11-20

At the start of this pass, CLI !10 and backend !484 pipelines were successful;
CLI !10 had no review comments. Backend review identified a missing move 409
response declaration, an old-response assertion in an authority test, and a CI
lane that could skip the SQL authority regressions. Both contracts now declare
the existing Conflict response. The new schema regression failed before that
addition. Actual generated and raw move commands preserve CONFLICT, send once,
and produce no success output. Backend response/query assertions and a required
isolated MySQL anti-skip lane were corrected in companion !484.

The GET target pair remains enforced by the backend. OAS independent Parameter
Objects do not support an Operation-level oneOf across query parameters. Both
parameter descriptions and the backend guide explain omitting both for full
reads, sending both for target reads, and a lone parameter returning 400. The
existing CLI flags and backend wire model are preserved; no local conditional
validation is claimed. Backend tests cover both lone-parameter directions and
an empty target, alongside valid full/target cases.

Local verification: full CLI Go1.25 race/shuffle 1063 passing records /428
top-level tests, no failures or skips; vet and build pass. Backend full affected
race/shuffle: sidebar 340/118 and SDK router 26/12, no failures/skips; the required
SQL lane executes 31/4 with no skips. Workflow guards, server build/vet and
OpenAPI checks pass. The new CI configuration passes GitLab's lint API. The
earlier 12 full-process cases were not rerun for these contract/test/CI changes;
deployed Auth/catalog/gateway acceptance is still separate. Live MR descriptions
record the new remote pipeline results when available.

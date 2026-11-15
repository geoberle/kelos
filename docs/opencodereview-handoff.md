# OpenCodeReview feedback and handoff

Inspected Alibaba OpenCodeReview revision `7e5ce06c88de5ce81df5dc4f24e2e485b517fae8`. OpenCodeReview is selected as the sole automated code reviewer, with combined Steve/David instructions; the configuration is implemented below; live acceptance remains outstanding.

## Existing outputs

The GitHub Action posts inline reviews with event `COMMENT`, attached to a commit, plus a maintained summary comment. Zero findings produces a summary without an inline review. It does not submit `REQUEST_CHANGES` or `APPROVE` reviews. [Posting implementation](https://github.com/alibaba/open-code-review/blob/7e5ce06c88de5ce81df5dc4f24e2e485b517fae8/scripts/github-actions/post-review-comments.js#L672)

By default it uploads `ocr-review-result-<workflow_run_id>-<run_attempt>` containing `ocr-result.json` and `ocr-stderr.log`. The JSON includes findings and a manifest identifying the reviewed head/base, terminal state, and coverage. Findings have file/line information, content, optional suggestions, and severity values critical/high/medium/low. [Artifact upload](https://github.com/alibaba/open-code-review/blob/7e5ce06c88de5ce81df5dc4f24e2e485b517fae8/action.yml#L1022), [finding schema](https://github.com/alibaba/open-code-review/blob/7e5ce06c88de5ce81df5dc4f24e2e485b517fae8/internal/model/review.go#L6), [manifest](https://github.com/alibaba/open-code-review/blob/7e5ce06c88de5ce81df5dc4f24e2e485b517fae8/internal/session/manifest.go#L243).

Workflow success alone is insufficient: partial results may exit successfully. Manifest states distinguish complete, partial, failed, and skipped. Complete describes selected review coverage, not the entire repository. No dedicated fixer handoff protocol was found; action outputs and artifacts provide integration inputs.

## Agreed poller handoff

Poll the configured trusted workflow, identify each run and attempt, and read its JSON artifact. Verify `manifest.input.resolved_head` matches the PR head before acting. Ignore obsolete results and wait for review of the current commit. Use findings as editor input, and manifest completion as evidence distinct from the findings themselves. Go publishes `review-done` only for a complete current-commit result with no must-fix findings or pending human feedback. Do not infer clean review from missing comments or a green workflow alone.

Partial, failed, or missing results block review completion. Failed or partial runs stop for manual rerun, with a concise explanation published by Go. A skipped review requires human `lgtm` rather than establishing `review-done`. The explicit severity mapping below implements our consequence-based [review policy](review-policy.md). This handoff is our proposed use of upstream outputs, not an upstream-prescribed agent protocol. Pin both the Action revision and CLI version when implementing.

Run the GitHub Action on ARC-managed self-hosted runners in the Kelos Kubernetes cluster. Runners connect outbound over HTTPS to receive jobs and upload results; no inbound internet endpoint is needed. See [ADR 0006](adr/0006-run-opencodereview-on-arc.md) and [GitHub network requirements](https://docs.github.com/en/actions/reference/runners/self-hosted-runners#requirements-for-communication-with-github).

## Implemented integration contract (kelos-v1)

`.github/workflows/ocr.yml` pins the inspected Action SHA above (upstream tag
v1.13.10) and independently pins npm CLI `1.13.10`. It runs only enrolled PRs
in `geoberle/kelos`, on the dedicated `kelos-ocr` scale set. Reuse in another
repository requires an explicit allowlist/runner URL change and installing the
trusted rules there. The trusted base supplies workflow and rules. The upstream
action checks out that base and fetches PR Git objects; no PR build, test,
local action, package installation or script is executed. Never add PR-head
checkout to this privileged workflow. Review source is untrusted model input.

Custom rules explicitly define `critical`/`high` as must-fix and `medium`/`low`
as advisory. This is our policy mapping, not an inference from upstream's
severity names. Missing/unknown severity or invalid finding structure blocks
completion. The model's correct application of this policy needs live acceptance.
The rules replace default prose and explicitly include `**/*`, including
`*_test.go`, `testdata/**` and `.golden` files. Binary, secret and oversized files
can still be excluded. A complete manifest only proves selected coverage:
inspect excluded/waived entries and do not authorize completion if relevant
changed tests/goldens or source were omitted without human acceptance.

Consumer contract:

- Identify the trusted workflow by repository and workflow ID/path, event
  `pull_request_target`, run ID and attempt. Do not use the run's head SHA as
  the PR SHA: this event runs in the base context. Bind the event PR identity
  separately and validate `manifest.input.resolved_head` against current PR head.
- Fetch exactly `ocr-review-result-<run_id>-<run_attempt>` from that run, containing
  `ocr-result.json` and `ocr-stderr.log`. Findings are top-level `comments`, with
  `path`, `start_line`, `end_line`, `content`, `severity` and optional suggestion
  fields. Manifest schema is `ocr.run-manifest/v1`; completion is
  `manifest.terminal_state`. Bind resolved base as well as head to the intended
  review range. OCR's own `manifest.run_id` is not the GitHub run ID.
  For the pinned full-range Action, `resolved_base` is the Git merge base of
  the fetched base branch and event PR head, not necessarily the PR API's
  current base SHA. Verify that range independently; comparing it directly to
  the base-branch tip would reject valid reviews after the branches diverge.
  The manifest does not carry the GitHub PR number or workflow run/attempt;
  those correlations must come from trusted GitHub evidence, never from
  findings, artifact naming alone, or OCR's session ID.
- Validate `coverage.selected`, `completed`, `reused`, `failed`, `waived`, and
  `excluded`. Preserve all findings even if inline publication cannot place them.
  Unknown schemas, malformed/missing artifacts and failed publication are not
  clean reviews. Artifact retention uses repository defaults: configure retention
  longer than the recovery window; expired evidence blocks completion.
- `complete` with acceptable coverage and zero must-fix findings is only an input
  to Go's review-done decision; pending human feedback still blocks it. `partial`
  and `failed` stop for explicit manual rerun; `skipped` needs human LGTM. Never
  infer success from exit zero, COMMENT reviews, or summary text.
- Rerun through GitHub Actions' rerun UI/API. Run attempts produce distinct
  artifacts. Reject stale heads and do not reuse a clean earlier attempt to
  conceal a later incomplete attempt. There is no automatic failure retry.
- Full-range reviews and nonincremental publication are selected. Reruns may
  create duplicate inline threads; sticky summaries are maintained upstream.
  `resolve_outdated: false` intentionally disables upstream's limited outdated
  bot-thread resolver. Humans resolve old threads, including after clean reruns;
  editors only reply with dispositions. All unresolved threads still block merge.

## Live acceptance still required

No live model or deployment test has run. In an isolated test repository/cluster,
verify runner image pull/startup, architecture, Node/npm installation permissions,
outbound connectivity and enforced network isolation; install trusted workflow
and rules on the base first. Use intended agent App/PAT credentials to create and
push an enrolled PR (events produced with GITHUB_TOKEN generally do not start
other workflows). Demonstrate a Go defect plus test/golden changes in selected
coverage, inline and unplaceable findings, clean repair rerun, attempt/head
correlation, duplicate/old thread behavior and manual resolution. Capture actual
JSON for complete, partial, failed and skipped reviews, plus missing artifacts,
and exercise the future Go consumer against these captures. Verify excluded
coverage and classification explicitly. Local render/lint checks do not satisfy
these acceptance criteria, and no credentials or registry changes are implied.

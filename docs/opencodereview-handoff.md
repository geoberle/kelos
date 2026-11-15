# OpenCodeReview feedback and handoff

Inspected Alibaba OpenCodeReview revision `7e5ce06c88de5ce81df5dc4f24e2e485b517fae8`. OpenCodeReview is selected as the sole automated code reviewer, with combined Steve/David instructions; integration is not implemented.

## Existing outputs

The GitHub Action posts inline reviews with event `COMMENT`, attached to a commit, plus a maintained summary comment. Zero findings produces a summary without an inline review. It does not submit `REQUEST_CHANGES` or `APPROVE` reviews. [Posting implementation](https://github.com/alibaba/open-code-review/blob/7e5ce06c88de5ce81df5dc4f24e2e485b517fae8/scripts/github-actions/post-review-comments.js#L672)

By default it uploads `ocr-review-result-<workflow_run_id>-<run_attempt>` containing `ocr-result.json` and `ocr-stderr.log`. The JSON includes findings and a manifest identifying the reviewed head/base, terminal state, and coverage. Findings have file/line information, content, optional suggestions, and severity values critical/high/medium/low. [Artifact upload](https://github.com/alibaba/open-code-review/blob/7e5ce06c88de5ce81df5dc4f24e2e485b517fae8/action.yml#L1022), [finding schema](https://github.com/alibaba/open-code-review/blob/7e5ce06c88de5ce81df5dc4f24e2e485b517fae8/internal/model/review.go#L6), [manifest](https://github.com/alibaba/open-code-review/blob/7e5ce06c88de5ce81df5dc4f24e2e485b517fae8/internal/session/manifest.go#L243).

Workflow success alone is insufficient: partial results may exit successfully. Manifest states distinguish complete, partial, failed, and skipped. Complete describes selected review coverage, not the entire repository. No dedicated fixer handoff protocol was found; action outputs and artifacts provide integration inputs.

## Agreed poller handoff

Poll the configured trusted workflow, identify each run and attempt, and read its JSON artifact. Verify `manifest.input.resolved_head` matches the PR head before acting. Ignore obsolete results and wait for review of the current commit. Use findings as editor input, and manifest completion as evidence distinct from the findings themselves. Go publishes `review-done` only for a complete current-commit result with no must-fix findings or pending human feedback. Do not infer clean review from missing comments or a green workflow alone.

Partial, failed, or missing results block review completion. Failed or partial runs stop for manual rerun, with a concise explanation published by Go. A skipped review requires human `lgtm` rather than establishing `review-done`. Classification of findings under our consequence-based [review policy](review-policy.md) remains open. This handoff is our proposed use of upstream outputs, not an upstream-prescribed agent protocol. Pin both the Action revision and CLI version when implementing.

Run the GitHub Action on ARC-managed self-hosted runners in the Kelos Kubernetes cluster. Runners connect outbound over HTTPS to receive jobs and upload results; no inbound internet endpoint is needed. See [ADR 0006](adr/0006-run-opencodereview-on-arc.md) and [GitHub network requirements](https://docs.github.com/en/actions/reference/runners/self-hosted-runners#requirements-for-communication-with-github).

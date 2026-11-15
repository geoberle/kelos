# Kelos setup and orchestration

Design discussion in progress. Implementation has not started.

## Agreed requirements

- Issue admission is deterministic: the issue-only `ready-for-agent` label expresses human readiness, and all blocking prerequisites must be fulfilled for admission. Eligibility is decided at native discovery; already-created Tasks continue after later changes.
- Repository selection is explicit.
- Deterministic Go orchestration runs as a dedicated Kubernetes Deployment in the same cluster as Kelos.
- For issue implementation, Go applies `agent-start` once admission conditions pass. A native Kelos TaskSpawner watches that label and owns Task creation and deduplication.
- Custom Go orchestration handles issue admission, review-feedback polling and Task scheduling, and merge authorization. OpenCodeReview reviews code; Kelos implements fixes. See [ADR 0004](adr/0004-poll-review-feedback.md) and [ADR 0005](adr/0005-use-opencodereview-as-sole-reviewer.md).
- Merge assessment evaluates suitability for automatic merging under policy, separately from tests and code-review approval. Its result includes a recommendation and a concise explanation of why or why not.
- The `lgtm` label displays validated approval: an authorized human posts `lgtm <full SHA>`, or Go consumes a favorable assessment. A bare label is not approval evidence. Approval is bound to a commit; the merge tool removes stale approval labels and ensures it merges only the approved commit. New commits invalidate approval from either source.

- For PR assessment, Go creates a Kelos Task for a specific PR commit, consumes and validates its structured result, and publishes the suggestion label and concise explanation. The assessment uses the full Codex harness with repository access, tools, skills, and agent capabilities; see [ADR 0003](adr/0003-assess-prs-through-kelos.md).
- Assessment starts only for non-draft PRs after required tests and reviews pass.
- The assessor may inspect code and run local checks, but must leave the PR branch and GitHub state untouched. Go owns publishing results and merge-related mutations.
- Go maintains one assessment comment per PR containing the assessed commit, recommendation, and concise explanation, including unfavorable results. The merge tool must recognize its own assessment record; arbitrary comments cannot authorize merging.
- Recommend automerge only for bounded, understandable, adequately tested changes without unresolved material risks. Uncertainty requires manual handling; change size alone does not establish safety.
- The `hold` label prevents automatic merging regardless of assessment or check results.
- An unfavorable assessment leaves the PR for human handling. New commits or an explicit request permit reassessment; unchanged work is not repeatedly assessed until a favorable answer appears. Execution failures and invalid output are separate retry cases.
- OpenCodeReview is the sole automated code reviewer, using combined Steve and David instructions and the [review policy](review-policy.md). Go coordinates review results and Kelos fixes until no must-fix findings remain. Go publishes `review-done` only after validating a complete OCR result for the current commit with no must-fix findings or pending human feedback. A later commit invalidates completion; execution failure does not establish completion.
- Go ignores OCR results for an obsolete PR commit and waits for review of the current commit. Stale findings do not automatically launch edits against newer code.
- Go polls review feedback and schedules Kelos Tasks, preserving feedback received during running Tasks. The earlier restriction on Go scheduling is superseded. Repair completion depends on review findings rather than a round limit; external-reviewer completion signals and exact scheduling rules remain to be defined.
- Allow only one editing Task per PR. Different PRs may progress independently; feedback arriving during execution remains pending for a subsequent Task.
- If a human pushes while an editor is working, the editor stops, reports the conflict, and preserves its work rather than overwriting or automatically reconciling the new changes. Task termination alone does not mark assigned feedback as handled.
- The editor returns an explicit disposition for each assigned finding: `fixed`, `already-satisfied`, or `needs-human`, with a concise explanation. Go tracks these outcomes; successful Task execution alone does not establish that feedback was handled. `needs-human` remains unresolved and prevents automatic review completion.
- The editor also publishes a concise reply to the original feedback. For inline review comments, reply in the existing review thread; for conversation comments, clearly reference the originating comment. Human-readable replies accompany the structured outcomes used by Go.
- The editor does not resolve review threads. Accepting the fix and resolving its thread remain the reviewer's or human's responsibility.
- Failed or conflicted editing Tasks stop for human intervention without automatically launching another editor. Preserve pending feedback and report the failure; successful OCR/fix cycles remain unlimited.
- Human intervention has two paths: an authorized human posts `lgtm <full SHA>` to authorize merging, or leaves a comment that triggers the fix-and-review loop without an explicit command.
- Merging with valid `lgtm` requires green CI, no `hold`, and no unresolved GitHub review threads. These conditions apply equally to human and automated approval. The merge operation does not additionally require `review-done`; that label gates automated assessment, not an existing approval.
- Initially authorize GitHub user `geoberle` for comment-triggered work and applying `lgtm`.
- Green CI means all checks required by the repository's GitHub rules have passed, with none pending. Reuse those requirements rather than maintain a separate check list.
- Go performs the merge directly once approval and merge requirements pass, restricting the operation to the approved commit instead of enabling deferred GitHub auto-merge.
- Use real merge commits, not squash merges.
- Version an explicit managed-repository configuration here, including the existing ready-for-agent label and agent settings. Store credentials in Kubernetes Secrets.
- Implementation agents apply `agent-managed` to their PRs to enroll them in the review and merge process. This is separate from the issue-only `ready-for-agent` label.
- Actionable feedback from an authorized human invalidates both `review-done` and `lgtm` before any new commit is pushed. The fix/review loop establishes fresh completion afterward.
- Assessment output is JSON with `commit`, `recommendation` (`approve` or `needs-human`), and a concise `reason`. Go rejects malformed results and commit mismatches; neither constitutes approval.
- Run issue admission and PR assessment/merging as separate reconciliation loops in one Go binary and Kubernetes Deployment.
- The cluster is unreachable from the internet; use outbound polling rather than inbound GitHub webhooks.
- Comments from explicitly authorized humans should trigger follow-up work. Bot status and assessment comments are excluded; the polling implementation remains to be determined.
- Store workflow evidence in GitHub comments and Kelos Task resources without a separate database. Human approval comments carry the full approved SHA.
- Review and repair may run on draft PRs. Assessment and merging require a non-draft PR, including when `lgtm` is present.
- Partial, failed, or missing review results block `review-done`; incomplete execution is not a clean review.
- Failed or partial OCR runs stop for manual rerun initially. Go publishes a concise explanation; this does not impose a limit on successful review/fix cycles.
- An OCR `skipped` result does not establish `review-done`. If no files were selected for review, merging requires human `lgtm` and the usual CI, draft, hold, and review-thread requirements.
- Run OpenCodeReview's GitHub Action on self-hosted Kubernetes runners managed by Actions Runner Controller (ARC), in the same cluster as Kelos. Communication with GitHub is outbound only; see [ADR 0006](adr/0006-run-opencodereview-on-arc.md).

## Open decisions

- How the merge operation enforces the commit-bound assessment record.
- Polling-based initial review and manual-feedback triggers; review completion evidence and invalidation when new feedback arrives. Earlier webhook-based proposals are superseded by the network constraint.
- Deployment, credentials, repository configuration, and task retention.

## Delivery requirements

Ship the Go binary in a published container image and the deployment Helm chart as a published OCI artifact. Provide lint/format tooling and CI following ARO-HCP templatize conventions, including its typed RawOptions → ValidatedOptions → completed Options flow. Deliver integration changes in Hearth targeting Hermine/k3s through its existing Ansible/Helm application deployment conventions. The newer Talos hearth cluster is outside this rollout's scope. Registry access and visibility must be chosen explicitly rather than inferred from the private source repository.

## Verified Kelos authorization behavior

At upstream commit `0f7b318ab66eee7f22079dbf0796dcb384ca9698`, polling issue/PR sources support `commentPolicy.minimumPermission` with read, triage, write, maintain, and admin roles. This authorizes configured comment commands, not arbitrary event discovery. Webhook filters support sender usernames but do not expose repository-role authorization. See upstream `api/v1alpha2/taskspawner_types.go` and `internal/source/github_comment_policy.go`.

## Verified polling constraints

Native `githubPullRequests` polling discovers PRs by label and can start initial review Tasks without inbound connectivity. Follow-up triggers support exact whole-line commands in comments/reviews, or qualifying current-head `changes_requested` reviews. Arbitrary prose comments and new commits alone do not retrigger completed Tasks. Commands support role-based authorization; review-state triggers do not apply that command authorization.

Retriggers must be newer than the existing Task's completion time, so feedback received during a running Task does not queue another run. SessionSpawner supports webhooks only. These limitations motivated the Go feedback poller in ADR 0004.

Before merging, Go rechecks mutable gates and submits the approved SHA as a precondition. The remaining race with newly arriving hold/feedback is accepted; repository requirements remain enforced without bypass.

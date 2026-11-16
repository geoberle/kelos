## Problem Statement

We need an issue-to-merge agent workflow that runs on Kubernetes inside a network unreachable from the internet. It must admit work only when issue dependencies are satisfied, implement changes, review and repair PRs, preserve human feedback, assess suitability for unattended merging, and enforce merge requirements without constant human coordination.

Kelos provides agent execution and issue discovery, but its native polling cannot reliably schedule work from arbitrary PR feedback or preserve triggers received during execution. A small Go component must own those deterministic decisions. Code review and merge assessment are separate judgments: a correct change can still warrant human authorization before merging.

## Solution

Deploy Kelos, a dedicated Go orchestration component, and ARC-managed GitHub Actions runners in the same Kubernetes cluster. All GitHub communication is outbound. Operate only on explicitly configured repositories.

Humans mark issues `ready-for-agent`. Go checks blocking prerequisites and applies `agent-start`; Kelos's native issue spawner implements the issue and creates a PR labeled `agent-managed`. Prefer one issue per PR; split larger work into independently implementable issues.

OpenCodeReview (OCR) runs through its GitHub Action on internal runners, using combined Steve and David instructions. Go consumes current-commit review results and schedules Kelos fixes. The cycle continues until every finding of the current review is addressed, for at most five repair rounds per PR. Go then publishes `review-done`, provided no human feedback remains pending.

For non-draft PRs with passing required CI and valid review completion, Go runs a separate merge-assessment Task in Kelos. A favorable assessment produces `lgtm`; an authorized human may also apply that same label. Go merges the approved commit with a real merge commit only when CI is green, the PR is non-draft, `hold` is absent, and all GitHub review threads are resolved. Human approval does not require `review-done` or favorable automated assessment.

## User Stories

1. As a maintainer, I want an explicit repository allowlist, so that automation does not affect unrelated repositories.
2. As an operator, I want all execution on Kubernetes with outbound-only GitHub access, so that no public cluster endpoint is required.
3. As a maintainer, I want to mark an issue `ready-for-agent`, so that I can express readiness before dependencies are satisfied.
4. As a maintainer, I want issue admission to check GitHub blockers deterministically, so that implementation starts only when prerequisites are fulfilled.
5. As an operator, I want Kelos's native issue spawner to consume `agent-start`, so that existing Task creation and deduplication are reused.
6. As a maintainer, I want agent-created PRs labeled `agent-managed`, so that their review and merge lifecycle is explicit.
7. As a maintainer, I want one issue to describe one PR-sized unit of work, so that partial-merge continuation is unnecessary.
8. As a maintainer, I want OCR to combine Steve's design priorities with David's correctness priorities, so that one review result covers both perspectives.
9. As a maintainer, I want findings classified by demonstrated consequences, so that style preferences do not create endless repair work.
10. As an operator, I want review findings and completion evidence tied to an exact commit, so that outdated results cannot authorize work or merging.
11. As a maintainer, I want review artifacts consumed even when findings cannot be posted inline, so that comment placement does not lose feedback.
12. As a maintainer, I want every review finding handed to a Kelos editor, so that review produces concrete corrections.
13. As a maintainer, I want review to run again after corrections, so that the editor's claim of success is independently checked.
14. As a maintainer, I want review/fix cycles to stop when findings are clear rather than after a fixed number of rounds, so that correct completion is the criterion.
15. As a maintainer, I want ordinary comments from authorized humans to initiate follow-up work, so that no special command is required.
16. As an operator, I want reporting-bot comments excluded from feedback triggers, so that automation does not trigger itself.
17. As a maintainer, I want feedback arriving during an edit retained, so that it is handled afterward rather than dropped.
18. As an operator, I want at most one editor per PR, so that agents cannot race while updating the same branch.
19. As a maintainer, I want independent PRs to progress concurrently, so that one PR does not block unrelated work.
20. As a maintainer, I want a concurrent human push to stop the editor safely, so that human changes are not overwritten or silently reconciled.
21. As a maintainer, I want each assigned finding to receive an explicit disposition and explanation, so that I know what was actually addressed.
22. As a reviewer, I want replies in the original inline review threads, so that fixes remain connected to the findings that prompted them.
23. As a reviewer, I want editors to leave thread resolution to the reviewer or human, so that reporting a fix is separate from accepting it.
24. As a maintainer, I want failed or conflicted editors to preserve work and pending feedback, so that intervention can resume from known evidence.
25. As an operator, I want repeated polls and restarts to avoid duplicate work, so that transient restarts do not repeat edits or model calls.
26. As a maintainer, I want `review-done` only after complete current-commit review and handled human feedback, so that the assessment handoff is meaningful.
27. As a maintainer, I want partial, failed, missing, and skipped reviews distinguished from clean reviews, so that missing evidence cannot grant approval.
28. As a maintainer, I want failures explained and manual reruns available, so that operational failures do not silently stall work.
29. As a maintainer, I want a separate complexity and safety assessment, so that suitability for unattended merging is judged independently from code correctness.
30. As an operator, I want that assessment to use the full Codex harness through Kelos, so that it can investigate the repository with tools and skills.
31. As a maintainer, I want both favorable and unfavorable assessments explained concisely on the PR, so that decisions are inspectable.
32. As an operator, I want malformed or stale assessment output rejected, so that Task success cannot accidentally imply approval.
33. As a maintainer, I want human and automated `lgtm` to mean the same approval, so that the merge path has one contract.
34. As a maintainer, I want human `lgtm` to bypass automated review/assessment requirements but preserve technical merge gates, so that I can exercise judgment explicitly.
35. As a maintainer, I want new commits and actionable feedback to invalidate prior approval, so that unapproved changes or outstanding corrections cannot slip through.
36. As a maintainer, I want `hold` to prevent merging, so that I can stop publication without manipulating CI or reviews.
37. As a maintainer, I want required CI, non-draft status, and resolved review threads enforced for every merge, so that approval does not bypass repository requirements.
38. As a maintainer, I want real merge commits, so that PR branch history is retained.
39. As an operator, I want versioned repository and agent configuration with credentials in Secrets, so that behavior is reproducible without committing credentials.
40. As an operator, I want durable workflow evidence without a separate database, so that the deployment stays small while retaining restart recovery.

## Implementation Decisions

### Execution ownership

- One Go binary and Kubernetes Deployment contains separate reconciliation responsibilities for issue admission, PR feedback/edit scheduling, and assessment/merging. Use direct SDK types and established Go/Kubernetes conventions rather than speculative wrapper layers.
- Kelos executes implementation, repair, and merge-assessment Tasks. Use the full Codex harness for assessment. Harness/model choices for other work remain configuration decisions.
- OCR is the sole automated code reviewer. Steve and David are combined prompt instructions, not two separately executed reviewers. Their former in-agent loop is replaced by OCR results followed by Kelos fixes, coordinated by Go.
- OCR runs as a GitHub Action on self-hosted ARC-managed Kubernetes runners in the Kelos cluster. Reuse its artifact and PR publishing integration. Pin the Action revision and OCR CLI version separately.
- Version the explicit repository and agent configuration. Keep credentials in Kubernetes Secrets. Initial authorized human identity is `geoberle`.

### Issue admission and enrollment

- `ready-for-agent` is issue-only human readiness. Go applies `agent-start` after all blockers are fulfilled. Native Kelos polling discovers those issues and owns implementation Task creation/deduplication.
- Implementation agents label their PRs `agent-managed`. The PR poller operates only on enrolled PRs in configured repositories.
- Preserve native issue deduplication when defining Task retention; deleting the Task used for deduplication must not silently become a retry policy.

### Code review contract

- Steve's priorities: unnecessary complexity and scope, architectural fit and ownership, concrete performance/resource impact, and simpler equivalent solutions.
- David's priorities: correctness and regression coverage; concurrency, retries, idempotency and partial failure; security/trust boundaries; API compatibility and Kubernetes conventions.
- Must-fix findings demonstrate a correctness, security, compatibility, or operational problem, or materially violate an agreed requirement. Each states a triggering scenario, consequence, and smallest suitable correction.
- OCR is instructed to report must-fix findings only. Whatever it reports, at any severity, is assigned to an editor and must be addressed before review completes.
- Read results from the configured trusted OCR workflow's JSON artifact, correlated with workflow run/attempt and reviewed commit. Consume findings and manifest completion separately. GitHub review state `COMMENT`, summary wording, or a green workflow alone is not a review verdict.
- Ignore obsolete results. A complete OCR result for the current commit, whose findings are all addressed, and no pending human feedback, permits Go to publish `review-done`. A finding is addressed when an editor answered it `fixed` or `already-satisfied`.
- Partial, failed, or missing results block completion. Failed/partial runs stop for manual rerun with a concise Go-published explanation. Skipped results require human `lgtm` rather than automatically granting review completion.
- Representation of must-fix classification in OCR's native severity schema remains an implementation validation item; do not silently equate an existing severity value with our policy.

### Feedback and repair

- Go polls authorized human feedback and review results, schedules Kelos Tasks, and preserves feedback received during execution. No inbound webhooks or additional quiet-period batching are required.
- Serialize editors per PR while allowing unrelated PRs to progress independently. Track exactly which feedback each Task was assigned; new feedback stays pending.
- Each assigned finding returns `fixed`, `already-satisfied`, or `needs-human`, with a concise explanation. Successful execution alone does not establish completion. `needs-human` remains unresolved.
- Publish human-readable replies as well as machine-consumable dispositions. Reply to inline findings in their existing threads; reference the originating comment for conversation feedback. The editor resolves the thread of each OCR finding it answers `fixed` or `already-satisfied`; `needs-human` threads and human feedback threads stay open.
- New actionable human feedback invalidates `review-done` and `lgtm` even without a code change. Bot status and assessment messages do not constitute new human feedback.
- Editors push directly to the PR branch with a regular push; force pushes are prohibited by instruction, not by a checked GitHub rule (ADR 0012). A concurrent human push stops editing rather than permitting overwrite or automatic reconciliation. Preserve work and report the conflict. Failed/conflicted editors await human intervention; pending feedback is retained.
- Review/fix cycles have no round limit. This does not mean failed executions automatically retry.

### Assessment and merge authorization

- Start assessment only for non-draft PRs after required CI and review completion. Use a commit-specific Kelos Task; inspect code and run local checks without modifying the PR branch or GitHub state.
- Assessment JSON contains `commit`, `recommendation` (`approve` or `needs-human`), and concise `reason`. Go validates the structure and commit; invalid output never grants approval.
- Approval requires a bounded, understandable, adequately tested change without unresolved material risks. Uncertainty requests human judgment. A line-count threshold alone does not establish safety.
- Maintain one assessment comment per PR with the commit, recommendation, and explanation, including unfavorable results. Unchanged work is not repeatedly assessed until it happens to receive approval; new commits or an authorized `reassess <full SHA>` comment targeting the current head permit reassessment. Failed executions and malformed results also require that explicit command; no automatic retries occur on an unchanged commit. Persist command identity to prevent duplicate attempts across polls and restarts.
- Go applies `lgtm` after favorable assessment. Authorized human `lgtm` is an equivalent approval and does not require `review-done` or a favorable assessment.
- Human approval is an authorized `lgtm <full SHA>` comment. Go validates that commit-bound evidence and displays `lgtm`, just as for automated approval. Applying the label directly grants no approval. Remove stale approval on new commits; never infer an approved head from a label event.
- For both approval sources, merge only a non-draft PR with valid `lgtm`, all repository-required CI checks passed and none pending, no `hold`, and no unresolved GitHub review threads. Respect repository enforcement rather than bypassing it.
- Go performs a direct merge with the approved commit as a precondition and uses a real merge commit. Do not enable deferred GitHub auto-merge. Recheck all gates immediately before the request. The operator accepts the remaining race with a hold or feedback arriving between the final check and merge; do not claim those conditions are atomic. Repository-enforced requirements still apply without bypass.

### Durable evidence

- No separate database. Use GitHub comments, Kelos Task resources, and OCR workflow artifacts as evidence. Maintain correlation between PR, commit, feedback, Task, assessment, and workflow run/attempt.
- Polling and restart recovery must be idempotent: avoid duplicate editors, repeated consumption of completed feedback, and duplicate assessment requests. Evidence retention and cleanup must preserve these properties.

## Testing Decisions

The user confirmed the primary test boundary: exercise the Go reconciliation behavior through observable GitHub mutations and Kelos Task creation/results. Use HTTP fixtures for GitHub and a Kubernetes API test environment. Assert external behavior, not helper layout, call ordering without semantic significance, or implementation-specific abstractions.

- Use table-driven Go tests and golden fixtures. Each case states its own intent; keep fixtures explicit rather than hiding scenarios behind elaborate shared helpers. There is no existing application or test suite in this repository to reuse.
- Admission cases: missing readiness, open/fulfilled blockers, excluded repositories, repeat polls, and existing implementation Tasks.
- Review cases: complete clean results, must-fix versus advisory findings, malformed/missing artifacts, stale commits, run attempts, partial/failed/skipped outcomes, and pending human feedback preventing completion.
- Scheduling cases: one editor per PR, concurrent unrelated PRs, feedback arriving during execution, restart after Task creation, and repeated observations without duplicated work.
- Editor outcomes: every assigned finding accounted for, missing dispositions remaining pending, needs-human outcomes, inline replies, no automatic thread resolution, failed execution, and concurrent human push without overwrites.
- Assessment cases: valid approval, needs-human explanation, invalid JSON, commit mismatch, duplicate requests, and invalidation while an assessment is running.
- Merge matrix: human/automated approval; stale approval; failing/pending CI; hold; draft; unresolved threads; unauthorized approval actor; missing review completion on the automated path; and permitted human approval without review completion. Test the approved-commit precondition when the head changes during reconciliation.
- Recovery cases: loss/restart of the process at external mutation boundaries, evidence retained across retries, failure explanations, and prevention of accidental retry after a stopped editor.
- Run one isolated cluster smoke scenario integrating Kelos, OCR on ARC, and a test GitHub repository. Demonstrate implementation enrollment, review findings, repair, fresh clean review, assessment, human intervention, and guarded merging with outbound-only connectivity.
- Deterministic tests require no live LLM. The integration smoke test validates actual harness/output and publication contracts; it is not a statistical evaluation of model quality.

## Out of Scope

- Multi-tenant service or implicit discovery of repositories to manage.
- Public webhook endpoints.
- A general workflow engine, fixed repair-round counting, or custom feedback debounce scheduling.
- Separate Steve and David agent executions or additional required automated code reviewers.
- LLM-based issue readiness decisions or dependency checks.
- Automatic merging based solely on labels without commit-bound evidence, or bypassing GitHub repository requirements.
- Automatic retry of failed/conflicted editing Tasks or failed/partial OCR runs in the initial version.
- Automatic thread resolution by the editor.
- A separate database, UI, or speculative SDK abstraction layer.
- Automatic continuation of a multi-PR issue after each partial merge.

## Further Notes

This specification synthesizes the latest decisions; earlier webhook-based, command-triggered, two-repair-round, and in-agent reviewer proposals are superseded. The repository currently contains design documents only; no implementation or cluster deployment has been performed.

The following are bounded validation and implementation decisions, not permission to weaken the agreed behavior:

1. **Human approval binding (resolved):** use `lgtm <full SHA>` comments; bare labels are not approval evidence.
2. **Merge races (resolved):** use repository enforcement and the SHA precondition, plus a final recheck of mutable gates. The remaining hold/feedback race is accepted and documented.
3. **OCR thread resolution (resolved):** the editor resolves the threads of findings it addressed; see ADR 0011.
4. **OCR policy and coverage:** encode consequence-based must-fix classification in the supported result contract. Explicitly include tests/golden fixtures where relevant rather than unintentionally inheriting exclusions. Confirm complete versus skipped coverage semantics for the chosen version.
5. **Evidence and failure recovery:** specify durable feedback identifiers, disposition schema, evidence retention, interrupted-editor work preservation, and explicit manual restart actions. Merge-assessment recovery uses authorized `reassess <full SHA>` comments without automatic retries on unchanged commits (ADR 0008).
6. **Operational configuration:** choose actual managed repositories, cluster/namespace details, model settings, credentials and bot identity, polling interval, resource limits, Task retention, and required repository rules. Ensure credentials used for agent pushes actually trigger the intended OCR workflow.
7. **Eligibility changes:** admission is decided at native Kelos discovery. Go withdraws `agent-start` on a later successful poll when readiness or blockers change; an already-created Task continues. No execution-time preflight or cancellation is required. Human intervention must preserve pending feedback rather than silently discard it.

Research inspected Kelos revision `0f7b318ab66eee7f22079dbf0796dcb384ca9698` and Alibaba OpenCodeReview revision `7e5ce06c88de5ce81df5dc4f24e2e485b517fae8`; these are evidence snapshots, not an automatic deployment-version selection. Relevant upstream sources: [Kelos](https://github.com/kelos-dev/kelos/tree/0f7b318ab66eee7f22079dbf0796dcb384ca9698), [OpenCodeReview](https://github.com/alibaba/open-code-review/tree/7e5ce06c88de5ce81df5dc4f24e2e485b517fae8), and [GitHub ARC documentation](https://docs.github.com/en/actions/concepts/runners/actions-runner-controller).

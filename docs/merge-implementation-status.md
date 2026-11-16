# Commit-bound human merging

`internal/merge.Poll` implements shared human and automated approval gates. Runtime execution is
opt-in through `configuration.mergeEnabled: true` and requires the dedicated
`controllerLogin`. The chart leaves merging disabled until repository enforcement
and credentials have been validated. No live merge has been performed during implementation.

The authorized human, initially `geoberle`, posts `lgtm <full SHA>`. Whitespace
around the command and hexadecimal case are normalized. The comment must be
unedited, target the exact current head, and have a current admin or maintain
repository role. A label alone is not approval. Human approval does not require
review-done. The controller writes a `kelos-merge/v1` record owned by its configured
identity and displays the shared `lgtm` label.

Each approval has a persistent invalidation record. Editing/deleting the source,
observing another head, later human feedback, or a later force-push/deletion/
restoration event invalidates it. Equal feedback timestamps are treated as
ambiguous and block approval. An invalidated comment never becomes valid again;
a fresh approval comment is required. Retain the controller's evidence comments.
Timeline read failures block merging rather than guessing history. Repository
roles are checked at reconciliation time, not reconstructed historically.

## Supported enforcement

The controller reads the effective rules of the target branch, which include
organization rulesets, and does not read classic branch protection. The target
must carry a `pull_request` rule, so nothing reaches it without a pull request.
A `required_linear_history` rule, or a `pull_request` rule whose allowed merge
methods exclude `merge`, stops the controller because it creates merge commits.
Other rules, bypass lists included, are not inspected; GitHub enforces them
when the merge is requested. Unavailable rules are not permission to merge.
GitHub also has to report the PR as mergeable and clean. Drafts, hold, closed
PRs, and unenrolled PRs do not merge.

No required checks are read from GitHub. Every check run and commit status on
the commit must be green; a skipped or neutral check counts as green, and a
commit without any check passes.
Pending, failed, cancelled and timed-out checks block this controller.
Check and legacy status pagination is handled. Test-merge commit results take
precedence when present; otherwise checks on the approved head are used. All
review threads must be resolved, including across GraphQL pages. Server-side
protection remains active when the merge request arrives.

The controller rechecks protection, CI, threads, approval, head and mutable PR
flags before submitting `merge_method: merge` with the approved `sha`. It never
requests deferred auto-merge or bypass. ADR 0007 accepts the remaining interval
in which hold or feedback can arrive after the last read. A SHA precondition
protects the head; it does not make these other conditions atomic.

After a failed or ambiguous merge response, the controller reads PR state before
considering another attempt. A merged PR is not merged again. A later poll of an
unmerged PR must repeat authorization and all gates.

## Verification and remaining acceptance

HTTP fixture tests exercise positive merges, private-plan 403, required-check
identity/pending/failure/missing results, test-merge precedence, unresolved threads,
admin bypass, unsupported rulesets, drafts, hold, stale heads, deleted/edited
approvals, arriving feedback, role loss, force pushes, persistent revocation after
a head returns, and ambiguous merge responses. Golden fixtures assert the exact
merge payload and retained revocation record. Race tests, formatting and lint run
with `GOTOOLCHAIN=go1.25.5`.

The operator made `hearthctl/kelos-orchestrator` public on 2026-11-16. The protection APIs no
longer return the plan-related 403: `main` returns “Branch not protected” and its
effective rules list is empty. Live merge acceptance still requires configured
required CI and merge enforcement. The separate `agent/**` rule protects editor
branches against force pushes and deletion, including administrators. The automated-assessment source
of approval uses this same gate contract; see [assessment](assessment.md).

References: [branch protection API](https://docs.github.com/en/rest/branches/branch-protection),
[effective rules](https://docs.github.com/en/rest/repos/rules#get-rules-for-a-branch),
[required status semantics](https://docs.github.com/en/pull-requests/how-tos/merge-and-close-pull-requests/troubleshooting-required-status-checks).

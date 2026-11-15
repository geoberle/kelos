# Commit-bound human merging

`internal/merge.Poll` implements the human approval path. Runtime execution is
opt-in through `configuration.mergeEnabled: true` and requires the dedicated
`controllerLogin`. The chart leaves merging disabled until repository enforcement
and credentials have been validated. No live merge or repository policy change
has been performed during implementation.

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

The initial supported policy is classic branch protection with admin enforcement,
strict required status checks, and required conversation resolution. Bypass
allowances and additional rulesets are rejected; unavailable or unsupported rules
are not permission to merge. GitHub also has to report the PR as mergeable and
clean. Drafts, hold, closed PRs, and unenrolled PRs do not merge.

Required CI contexts are read from protection, including GitHub App bindings.
Missing, pending, failed, skipped or neutral required checks block this controller:
it requires success even though GitHub itself permits skipped/neutral results.
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

The real private `geoberle/kelos` repository currently returns a plan-related 403
for both protection APIs. Live positive/negative merge acceptance therefore
remains blocked until an appropriate repository with enforcement is available.
No visibility or subscription change is implied. The automated-assessment source
of approval is implemented by issue #9 and must use this same gate contract.

References: [branch protection API](https://docs.github.com/en/rest/branches/branch-protection),
[effective rules](https://docs.github.com/en/rest/repos/rules#get-rules-for-a-branch),
[required status semantics](https://docs.github.com/en/pull-requests/how-tos/merge-and-close-pull-requests/troubleshooting-required-status-checks).

# Issue #8 implementation status

The `internal/merge.Poll` entry point is a partial implementation, not ready for wiring or acceptance. It discovers enrolled PRs in explicit repositories, checks exact human approval comments and current admin/maintain role, and records controller-authored approval evidence. It deliberately does not send merge requests. No live policy or merge mutation was attempted.

The HTTP test currently proves only that discovery failures (403 and 500) do not mutate GitHub. It does not yet prove protection enforcement, positive merges, final gate revalidation, approval invalidation across pushes/restarts, rejected SHA handling, or ambiguous response recovery. These remain required work; passing the repository checks does not constitute issue #8 acceptance.

The previously observed private-repository plan-related 403 is a live acceptance gap only. Deterministic fixtures must still provide supported enforceable protection and positive merge cases. It is not a reason to disable implementation work.

The intended supported protection contract is classic branch protection with admin enforcement, strict required status checks and conversation resolution; unknown/unavailable requirements must fail closed. This contract has not yet been implemented. References: https://docs.github.com/en/rest/branches/branch-protection and https://docs.github.com/en/rest/repos/rules#get-rules-for-a-branch.

ADR 0007's accepted final-recheck hold/feedback race remains unchanged.

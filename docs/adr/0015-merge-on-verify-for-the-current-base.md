# Merge on a verify result for the current base

A green `verify` check says the pull request passed on the base as it was when
the run started. Two pull requests that are each green can break the default
branch together. GitHub merge queues would prevent this but are not available
to private repositories on the Team plan.

The controller follows Prow's Tide. It merges only when `verify` passed for the
pull request head on the commit the base branch points to now, the tested
base. When the base has moved, it runs `verify` again on the new base and
waits. The branch is never rewritten, so the review, its evidence and the
approval stay valid. Requiring an up-to-date branch was rejected: every rebase
is a new head that must be reviewed and assessed again, and each merge would
outdate every other open pull request.

The controller pins the base, as Tide does. It dispatches the repository's
`ci.yml` on the base branch with the head and base commits as inputs, and
records the run as a pending commit status `verify-base` on the head. The
status description ends in ` BaseSHA:<commit>`, the form Prow uses, and its
target is the run. When the run completes the controller writes the result as
the same status. The pending status is the only record of the run, so a head
and base pair is verified once; a base that moves again starts a new run.

The controller reports the result, not the workflow, as crier does in Prow. A
`pull_request` workflow is defined by the pull request, so a report job in it
could grant itself the right to write any status. The dispatched workflow is
the one on the base branch and needs a read-only token. Only statuses written
by the controller identity count.

One pull request per base branch is verified at a time, the lowest number first, and
only after every other gate has passed. Each merge moves the base, so parallel
runs would be outdated by the first merge. Tide also batches; this controller
does not.

A red result blocks the merge until the head or the base changes. A pull
request that conflicts with its base is not verified; it carries the
`needs-rebase` label for as long as GitHub reports the conflict.

The `verify` check of the `pull_request` run stays as the early signal on every
push. Every repository the controller merges in therefore needs the caller
workflow. This supersedes the statement in ADR 0012 that a repository without
CI merges on review evidence and approval alone.

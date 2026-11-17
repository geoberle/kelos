# Bind commands to the observed head

Humans approve with `/lgtm` and request another assessment with `/reassess`,
without naming a commit. This follows Prow, where the bot does the commit
bookkeeping. It supersedes the typed commit of ADR 0007 and ADR 0008.

The controller binds `/lgtm` to the PR head it first observes and records that
commit. The approval counts only if GitHub's push log for the branch shows that
the head was put there before the comment and nothing was pushed since. A push
after the approval is therefore not approved, including one that restores a
commit the branch held earlier. A branch in a fork has no push log in the
repository and cannot be approved this way. One gap remains and is accepted: a
commit pushed shortly before the comment, which the approver had not yet seen,
is approved. The recorded commit makes that visible.

`/reassess` counts only when the latest assessment is of the current head and
the comment is newer than it, so it always answers that assessment. A new head
is assessed without a request.

Both commands must be the whole, unedited comment of the authorized human.

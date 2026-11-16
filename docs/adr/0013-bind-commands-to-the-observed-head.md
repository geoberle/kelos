# Bind commands to the observed head

Humans approve with `/lgtm` and request another assessment with `/reassess`,
without naming a commit. This follows Prow, where the bot does the commit
bookkeeping. It supersedes the typed commit of ADR 0007 and ADR 0008.

The controller binds `/lgtm` to the PR head it first observes and records that
commit. GitHub does not expose when a commit was pushed, so the approval counts
only if a check suite for the head on its branch was created before the
comment. A push after the approval is therefore not approved, and a repository
without any workflow cannot be approved this way. One gap remains and is
accepted: a commit pushed shortly before the comment, which the approver had
not yet seen, is approved. The recorded commit makes that visible.

`/reassess` counts only when it is newer than the latest assessment of the
current head, so it always answers that assessment. A new head is assessed
without a request.

Both commands must be the whole, unedited comment of the authorized human.

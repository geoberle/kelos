# Bind human approval to an explicit commit

Humans authorize merging with `lgtm <full SHA>` comments. GitHub label events cannot reliably recover the approved head through polling downtime. Go validates the comment and displays the same `lgtm` label used for automated approval; a manually applied label alone grants no authority.

Go rechecks merge gates immediately before submitting a real merge-commit request with the approved SHA. Repository rules apply without bypass. The operator accepts the remaining interval in which hold or feedback may arrive after the recheck; the SHA precondition does not make those mutable gates atomic.

Issue eligibility remains native discovery-time admission. An already-created Kelos Task continues after subsequent eligibility changes; no pre-start check is required.

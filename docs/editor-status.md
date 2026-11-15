# OCR editor

Optional `configuration.editor` enables Task scheduling with `namespace`,
`workspace`, `agentConfig`, and `secret` (Codex OAuth Secret name). Each is required;
`controllerLogin` must also be configured. The namespace, Workspace, AgentConfig,
and Secret must already exist. The Role allows only reading and creating Tasks.
No live installation is enabled by default.

The direct client-go v0.34.1 dynamic client uses Kelos v1alpha2 Task resources,
without importing upstream's Go 1.27 module. CurrentEvidence revalidates OCR
artifacts instead of trusting retained status text as fresh authority. Assignments
persist repository, PR, run, attempt, head and indexed finding IDs in immutable
Task annotations. Tasks have no TTL. A retained failed, waiting, running, malformed,
or needs-human Task prevents further scheduling for that PR. Different PRs have
independent Tasks. Fully accounted successful results allow a new head's verified
OCR findings to start another Task; there is no repair-round limit.

The prompt requires editing from the assigned head, a remote-head recheck, and
regular push only. Divergence stops work and preserves the workspace; no force
push or rebase onto human work is allowed. These are agent execution instructions,
not an independently enforced Git transport sandbox. Use the existing OCR Action
on synchronize with credentials that trigger Actions. The editor does not merge.

`status.results.editor-result` is single-line JSON with `head`, `commit`, and
`dispositions` containing `id`, `outcome`, `explanation`. Outcomes are `fixed`,
`already-satisfied`, `needs-human`. The controller rejects incomplete accounting,
checks commit ancestry, publishes correlated explanations referencing the OCR
summary, and never resolves threads. Needs-human remains pending. Keep Task and
workspace retention intact; deletion is not a retry mechanism.

Deterministic coverage currently exercises enrollment and retained Task stopping
through GitHub and Kubernetes HTTP endpoints, including repeated polls, disposition validation, ancestry rejection, needs-human
reporting, and golden reply correlation. Real API
server/envtest validation, successful full-cycle fixtures, inline thread correlation, and live output capture remain pending.
No live cluster credentials were used and no activation was performed. Run one
controller instance; multiple concurrent controllers are not yet supported.

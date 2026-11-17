# OCR editor

Optional `configuration.editor` enables Task scheduling with `namespace`,
`workspaces`, `agentConfig`, and `secret` (Codex API-key Secret name). Each is required;
`controllerLogin` must also be configured. `workspaces` maps every configured
repository (`owner/repo`) to its Kelos Workspace. The namespace, Workspaces,
AgentConfig, and Secret must already exist. The Role allows reading, creating and patching
Tasks; a Task whose result is validated, answered and receipted is annotated
`orchestrator.kelos.dev/result-recorded: "true"` and not processed again.
No live installation is enabled by default.

The direct client-go v0.34.1 dynamic client uses Kelos v1alpha2 Task resources,
without importing upstream's Go 1.27 module. CurrentEvidence revalidates OCR
artifacts instead of trusting retained status text as fresh authority. Assignments
persist repository, PR, run, attempt, head and indexed finding IDs in immutable
Task annotations. Tasks have no TTL. A retained failed, waiting, running, malformed,
or needs-human Task prevents further scheduling for that PR. Different PRs have
independent Tasks. Fully accounted successful results allow a new head's verified
OCR findings to start another Task, for at most five rounds per PR.

A failed `verify` check on the current head is editor work too. The controller
reads the newest `verify` check run of the head; when it completed with
`failure`, it starts a Task whose single finding carries the last 60 lines of
the failing job's log up to its final error. The controller fetches the log
because the editor's GitHub identity cannot read workflow logs. The finding ID
is `check/<owner>/<repo>/<pr>/<check run>`, so one failed run is assigned once;
a repair that fails again is a new run and a new Task, for at most three per
PR. Human feedback is handled first, a failed check before OCR findings. A red
`verify-base` status is not repaired.

The prompt requires editing from the assigned head, a remote-head recheck, and
regular push only. Divergence stops work and preserves the workspace; no force
push or rebase onto human work is allowed. These are agent execution instructions,
not an independently enforced Git transport sandbox. The controller does not
check protection of the editor's branch (ADR 0012). Use the existing OCR Action
on synchronize with credentials that trigger Actions. The editor does not merge.

The editor's final message, which Kelos stores in `status.results.response`,
is one JSON object with `head`, `commit`, and
`dispositions` containing `id`, `outcome`, `explanation`. Outcomes are `fixed`,
`already-satisfied`, `needs-human`. The controller rejects incomplete accounting,
checks commit ancestry and membership in the current PR branch, publishes
explanations as replies to uniquely correlated OCR inline comments or references
the exact published OCR summary. The controller's credential cannot resolve
review threads; the editor resolves the thread of each OCR finding it answers
`fixed` or `already-satisfied`. Inline correlation
requires the run/attempt marker, original commit, path, lines, and finding text;
ambiguous matches stop scheduling. Reply deduplication accepts only the configured
controller identity. Needs-human remains pending. Keep Task and
workspace retention intact; deletion is not a retry mechanism.

Deterministic coverage exercises enrollment, successful Task creation using the
v1alpha2 worker schema and API-key credentials, retained Task stopping, precise
inline correlation, ambiguous-correlation rejection, disposition validation,
unpublished-result rejection, needs-human reporting, inline replies, copied-reply
rejection, restart deduplication, and golden summary replies through GitHub and
Kubernetes HTTP endpoints. Real API server validation passed with Kubernetes 1.34.1 envtest binaries and the
upstream v0.64.0 Task CRD, disabling conversion for same-version operations.
The schema accepts worker.agentConfigRefs; the referenced AgentConfig must still
be provisioned for execution. Set EDITOR_TEST_KUBECONFIG to a disposable API server
containing that CRD to exercise creation and restart retention (the test creates
namespace agents). Completed-attempt and fresh-attempt fixtures cover restart
scheduling on the same head. Full push/review cycle and live output capture
remain unverified.
No live cluster credentials were used and no activation was performed. Run one
controller instance; multiple concurrent controllers are not yet supported.

Task identity includes the head, OCR run and attempt. Retained assignments also
provide deduplication for Tasks created by earlier controller versions.
Assignment, Result and Disposition are the durable JSON contract. Result.Validate
checks complete unique accounting and explanations; consumers must additionally
verify publication/ancestry and keep needs-human dispositions pending. Human
feedback scheduling and validated receipt consumption are described in
[human feedback](feedback.md).

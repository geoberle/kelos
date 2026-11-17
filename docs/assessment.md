# Merge assessment

Set optional `configuration.assessment` with `namespace`, `workspaces`,
`agentConfig` and `secret`, plus the dedicated `controllerLogin`. `workspaces`
maps every configured repository (`owner/repo`) to its Kelos Workspace. The
namespace, Workspaces, AgentConfig and Codex API-key Secret must already exist.
Use separate Workspaces whose GitHub credential can only read the repository;
never reuse the editor's write credential. The assessor may investigate using local
files, tools and skills, but must not mutate GitHub or publish branch changes.
Credential permissions require verification before activation.

The Go loop requires an enrolled, non-draft open PR, no hold, green required CI,
and freshly validated complete OCR evidence with no pending human feedback.
`review-done` alone does not prove those conditions. It creates a retained Kelos
v1alpha2 Task for the exact head and request identity. Task creation and result
consumption survive restarts. No TTL is assigned; replacing a retained Task with
a different Kubernetes UID is rejected.

The Codex final response is read from base64 `status.results.response`. It must
be exactly one JSON object with string fields `commit`, `recommendation` and
`reason`. The commit must match, the recommendation is `approve`, `needs-fix`
or `needs-human`, and the reason must be nonempty. Unknown/duplicate fields, trailing JSON,
malformed output and oversized results do not approve. Task success alone is
not approval.

A head that the human approved with a still valid `/lgtm` is not assessed: the
decision is made, and an assessment would only repeat or contradict it. An
explicit `/reassess` still starts one. A check that is still running on the
commit is waited for and not logged as a failure.

`needs-fix` is for a concrete defect in the change that the assessment can
state with its trigger and consequence and that a code change can repair. The
editor takes the reason as a finding, `assessment/<owner>/<repo>/<pr>/<task>`,
and repairs it; the new head is reviewed and assessed again. At most two such
repairs run per PR, then the controller reports once and waits for a person.
`needs-human` remains for what needs judgement: an accepted risk, licensing,
behaviour that only hardware shows.

A single controller-owned `kelos-assessment/v1` PR comment retains attempt
identity, Task UID, head, request, state and concise explanation. Terminal
failed, malformed and needs-human attempts never automatically retry on the
same commit. An unedited authorized `/reassess` comment that is newer than the
latest assessment of the current head permits a fresh attempt; the initially authorized user is
`geoberle`, with current admin/maintain permission. Repeated polls of that command
reuse its attempt. New heads may receive an initial assessment when their gates
pass. Keep both history and Tasks; deleting evidence is not a retry mechanism.

Favorable evidence displays the same `lgtm` label as human approval. The shared
merge authorization path checks current head, later feedback, review/push events
and explicit-request authority. Invalidated attempts remain invalidated. Human
approval remains independent of automated review completion. Actual merging is
still separately controlled by `configuration.mergeEnabled` and always uses the
existing CI, hold, thread and exact-SHA gates.

HTTP/Kubernetes-boundary fixtures exercise retained Task creation, valid and
unfavorable results, strict parsing, terminal failure, explicit retry,
unauthorized/edited requests, publication-time head changes and shared merge
approval. Live Codex output, read-only credential enforcement, full cluster
execution and end-to-end merge acceptance remain outstanding.

# Human feedback

The editor polls ordinary conversation comments, inline comments and submitted
review bodies from `geoberle`, checking the current admin/maintain role before
admitting new requests. Empty text, bot reports and comments that carry a
control command are not repair requests. A comment carries `/lgtm`,
`/reassess` or `/kelos run` when one of its lines starts with the command;
whatever else the comment says is a remark, not a request. A comment with a
`/lgtm cancel` line, Prow's withdrawal, never approves. OCR findings remain on
their separate verified-artifact path.

Each observed request receives an identity containing its source kind, GitHub ID
and a digest of its version and text. The dedicated controller persists the
request in a `kelos-feedback/v1` comment before scheduling work, including while
an editor is already running. Editing a source creates a new version. Deleting a
source does not erase an already observed request. Feedback posted and deleted
entirely between polls cannot be recovered from GitHub's current-comment API.

The latest word wins. A request the human wrote before their newest `/lgtm`
is dismissed: it is no longer pending, starts no editor Task and withdraws no
label, and the controller names the dismissed requests once in a
`kelos-dismissed-feedback/v1` comment. A request written or edited after the
approval stays pending and withdraws that approval. A finished editor Task
whose requests are all gone is marked as recorded and no longer holds the pull
request back.

The existing editor handles pending requests on the current PR branch with one
Task per PR at a time. Later requests remain pending. New pending feedback
withdraws `review-done` and `lgtm`; it cannot be cleared merely because a Task
succeeded. Results must account for every assigned finding, verify commit ancestry
and current-branch membership, and publish the correlated explanations. Inline
replies remain in their original threads; the editor resolves only the threads
of OCR findings it addressed, never human feedback threads.

After successful validation and publication, the controller persists a
`kelos-handled-feedback/v1` receipt containing the Task, result commit and exact
handled finding IDs. Review completion accepts only receipts from the configured
controller and rechecks that their commits remain on the PR branch. Copied
comments from other authors confer no authority. Retain observation comments,
receipts and Kelos Tasks; they provide restart deduplication without a database.
Run a single controller instance.

A failed, malformed or needs-human editor remains stopped and keeps its feedback
pending. To recover without restarting automation, preserve its workspace and
commits, finish the corrections manually on the existing PR, run CI and OCR,
resolve review threads, and post a fresh authorized `/lgtm` when ready.
Human approval bypasses `review-done` but not CI, hold or unresolved-thread gates.
Deleting Tasks or evidence is not a supported retry mechanism. Automated editor
restart is intentionally absent; `reassess` retries only merge assessments.

HTTP fixtures cover request scheduling, retained observations after source
removal, feedback during execution, label invalidation, completed-task
idempotency, controller receipt ownership and commit membership. Full live
workflow acceptance and deployment remain pending.

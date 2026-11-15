# OCR evidence consumer

The Go runtime reconciles enrolled PRs when `configuration.controllerLogin` is
set. An empty login keeps review reconciliation disabled for admission-only
installations. Use a dedicated controller identity, separate from `geoberle`,
OCR and implementation agents, with Actions read, pull requests read, and issues
write access. The credential must publish comments as that identity. A configured
bot login supports installation tokens without calling the user-only `/user`
endpoint. Token issuance and renewal remain deployment responsibilities; the
current process reads its token at startup.

The consumer validates the trusted OCR workflow, PR/run/attempt identity, current
head and merge-base range, complete changed-file coverage, findings, and successful
publication. It does not interpret a green workflow as a clean review. Unknown
severity, ambiguous JSON, incomplete coverage, missing/expired artifacts and
publication failures prevent completion. File-level findings are supported;
explicit null comments are a valid empty list in the pinned OCR contract.

The workflow publishes an immutable identity artifact before OCR, then a separate
publication artifact containing the actual reviewed merge base and the pinned
Action's publication outputs. The consumer checks its failure count and summary
comment against GitHub. The run name identifies the PR when GitHub omits the PR
association on a pull_request_target run. Missing identity on an identifiable
current-PR run blocks completion instead of falling back to an older review.

A controller-authored `kelos-review/v1` status comment retains the exact OCR JSON
and repository, PR, run, attempt and head identity. Findings can be addressed by
that identity plus their zero-based position in `result.comments`; content is
preserved, including findings that OCR places in its summary. Transient failure
status preserves the previous evidence block without granting it validity.
Oversized evidence requires human attention rather than truncation. The editor
must validate current head/run and trusted authorship before consuming it and
persist assigned finding identities in its Kelos Task. Artifact expiration may
block new completion even when the historical evidence remains visible.

Invalid completion is withdrawn before fallible status writes. Before applying
`review-done`, the consumer rechecks the latest evidence, run attempt, PR head,
base and human feedback. Downstream assessment must validate this evidence again;
labels alone are not authority. These checks narrow external-state races but are
not atomic GitHub transactions.

Until issue #7 implements validated feedback dispositions, authorized human
feedback remains pending conservatively. No editor or merge behavior is implied
by this slice. Failed/partial review requires a manual workflow rerun; skipped
review requires human approval. A new head needs fresh review.

Verification: table-driven HTTP tests use real ZIP downloads and a golden status
fixture. They cover clean/advisory completion, restart idempotency, must-fix and
unknown severity, partial/skipped results, malformed/duplicate JSON, stale heads,
new runs/attempts, publication failures, unrelated runs, pagination, expired
artifacts, identity mismatch and arriving feedback. Go race tests, formatting and
lint use `GOTOOLCHAIN=go1.25.5`.

Live OCR/ARC publication, GitHub permissions, installation-token provisioning and
cluster execution remain unverified. Issue #5 is not live-acceptance complete.

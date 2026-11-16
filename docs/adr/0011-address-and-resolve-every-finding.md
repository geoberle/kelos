# Address and resolve every finding

Every finding the reviewer reports is assigned to an editor, whatever its
severity, and review completes only when each is addressed. The reviewer is
instructed to report must-fix findings only, so that nothing is posted that is
not worth repairing.

The editor resolves the review thread of each OCR finding it answers `fixed` or
`already-satisfied`. A fix is checked independently by the review of the new
commit. An `already-satisfied` answer changes nothing and triggers no review, so
it closes the finding on the editor's judgment alone; this is accepted to keep
the loop unattended. `needs-human` threads and human feedback threads stay open.

The editor, not the controller, resolves threads because the controller's
credential has no write access to repository contents, which GitHub requires
for resolving a thread.

Reviews are not deterministic and may raise new findings on every commit. At
most five editor Tasks are started for review findings on one PR; after that
the PR waits for a human.

This replaces the earlier rules that only must-fix findings are repaired, that
the repair loop has no round counter, and that editors never resolve threads.

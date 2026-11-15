# OCR consumer implementation status

The preliminary consumer in internal/review is intentionally not called by the
runtime. Issue #5 is not complete.

Implemented: direct go-github polling of enrolled PRs, trusted workflow ID/path
and event checks, run/attempt identity artifact consumption, bounded ZIP reads,
manifest range checks through GitHub compare merge-base, coverage partition and
changed-file checks, severity validation, conservative human feedback blocking,
and authored status comments.

The workflow now uploads event identity before OCR without executing PR code.
This identity captures the event base SHA; OCR fetches the base branch later.
A branch movement between these operations can conservatively reject a valid
review. Capture the actual fetched base tip in trusted metadata to close this gap.

Required before runtime activation:

- Complete table-driven/golden HTTP coverage for valid ZIP results, latest
  attempts, identity spoofing, stale heads, all terminal states, publication
  failures, pagination, restart and mutation-boundary failures.
- Make missing identity on unrelated workflow runs avoid repository-wide
  starvation without falling back to an older review for the same PR.
- Verify upstream publication success independently of workflow success.
- Preserve exact artifact/run/finding references in durable editor handoff.
- Replace conservative permanent human-feedback blocking with the approved
  disposition contract when that consumer exists.
- Ensure invalidation precedes status publication even if publication fails.
- Recheck run attempt and feedback immediately before publication; define the
  downstream requirement to revalidate durable evidence rather than trust labels.
- Detect truncated JSON/downloads explicitly and reject duplicate JSON keys.
- Add minimal existing-options runtime wiring after the safety tests pass.

Validation so far: go test -race ./... passes; only empty-repository and
missing-artifact withdrawal scenarios are covered for review.
Pinned golangci-lint v2.8.0 fails under local Go 1.27.3 because its importer does
not support export data version 5. Run it under the repository-compatible Go
toolchain before accepting this slice. No live OCR acceptance was performed.

# Review policy

OpenCodeReview is the sole automated code reviewer. Combine Steve and David's prompt instructions into one review configuration with the complementary priorities below. These are review perspectives, not separately executed agent skills.

## Steve: design and maintainability

- Unnecessary complexity, abstractions, and scope.
- Ownership boundaries and fit with existing architecture.
- Performance and resource use with concrete impact.
- Simpler solutions that preserve required behavior.

## David: correctness and operational safety

- Incorrect behavior, edge cases, and missing regression coverage.
- Concurrency, retries, idempotency, and partial failures.
- Authentication, authorization, secrets, and trust boundaries.
- API compatibility and Kubernetes reconciliation conventions.

## Findings and completion

Classify findings by demonstrated consequence:

- **Must fix:** a concrete correctness, security, compatibility, or operational problem, or a material violation of an agreed requirement. Blocks `review-done`.
- **Should improve:** a meaningful maintainability or efficiency improvement without a demonstrated blocking consequence. Report it without prolonging the repair loop.
- **Optional:** style, preferences, or speculative improvements. Usually omit these.

Every must-fix finding states the triggering scenario, consequence, and smallest suitable correction. Review and repair continue until OpenCodeReview reports no remaining must-fix findings; the topic of a finding alone does not establish severity.

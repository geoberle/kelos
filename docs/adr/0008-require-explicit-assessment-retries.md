# Require explicit assessment retries

An authorized human requests reassessment with `reassess <full SHA>`, targeting
the current PR head. This command also recovers failed executions and malformed
assessment results. There are no automatic retries on an unchanged commit,
including after an unfavorable recommendation. New commits may receive a new
assessment once the normal CI and review-completion gates pass.

Persist the request identity with the assessment Task so repeated polls and
controller restarts do not turn one command into multiple attempts. An explicit
request does not bypass authorization, CI, review completion, or stale-result
validation.

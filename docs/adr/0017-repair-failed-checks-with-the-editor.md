# Repair failed checks with the editor

An implementation agent runs `make verify` before it opens a pull request.
`make e2e` needs a container runtime the agent pod lacks and runs only in CI
(ADR 0014). A failure there appeared after the pull request was opened, and
nothing acted on it: the editor took work only from OCR findings and human
feedback, so the pull request waited for a person.

The controller treats a failed `verify` check on the current head of an
agent-managed pull request as a finding for the editor. The finding carries the
end of the failing job's log, which the controller fetches. The editor's own
GitHub identity has no right to read workflow logs, and giving the controller's
excerpt keeps it that way. The log is untrusted input like any finding content.

Letting the implementation agent start the CI workflow on its branch and wait
for the result before opening the pull request was rejected. It needs the right
to start workflows on the worker identity and does not cover a failure that
appears later, after a rebase or a fix.

One failed check run is assigned once. A repair whose push fails the check
again is a new run and gets a new Task, for at most three per pull request;
then the controller reports once and waits for a person. These repairs do not
count toward the five review rounds. Human feedback comes first, a failed check
before OCR findings, because a new head is reviewed again anyway.

Not covered: a red `verify-base` result, and an editor that answers a failed
check without pushing a change. In both cases the pull request waits for a
person, as before.

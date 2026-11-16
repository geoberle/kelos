# Agent Workflow

Terms for admitting issue work and assessing pull requests for automatic merging.

## Language

**Issue admission**:
The decision that an issue marked ready for an agent has all its blocking prerequisites fulfilled and may start implementation.

**Merge assessment**:
An evaluation of a pull request's complexity and safety that recommends whether it is suitable for automatic merging.

**Merge authorization**:
The decision that a pull request satisfies the requirements for automatic merging. A favorable assessment is an input to this decision.

**LGTM**:
Approval of a particular PR commit for merging, given by an authorized human or automated assessment. Both sources carry the same meaning; CI and hold requirements still apply.

**Agent-managed PR**:
A pull request enrolled in automated review, repair, and merge processing, identified by `agent-managed`.

**Ready for agent**:
Human-declared issue readiness, identified by `ready-for-agent`. Blocking prerequisites may still prevent admission.

**Agent start**:
Issue admission granted after readiness and blocker checks, identified by `agent-start`.

**Run command**:
The issue comment `/kelos run` that starts an implementation Task. Issue admission posts it once; an authorized human posts it to retry a stopped Task.

**Stop report**:
The comment on an issue stating why its implementation Task ended without completing the issue, which acceptance criteria are unmet, and how to proceed.

**Must-fix finding**:
A demonstrated correctness, security, compatibility, or operational problem, or a material violation of an agreed requirement. The only kind of finding the reviewer is asked to report.

**Addressed finding**:
A review finding an editor answered as fixed or already satisfied. Every finding of the current review must be addressed before review completes.

**Feedback disposition**:
The explicit outcome of addressing a finding: fixed, already satisfied, or needs human attention, accompanied by a concise explanation. A finding needing human attention remains unresolved.

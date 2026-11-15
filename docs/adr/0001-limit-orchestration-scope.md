# Limit orchestration to issue admission and merge authorization

Status: superseded by ADR 0004.

Use Kelos for implementation and event-triggered, agent-managed review and repair; custom Go orchestration handles issue admission and PR merge authorization only. Native Kelos triggers initiate work, and agents review and fix until no P1/P2 findings or blockers remain, without custom feedback batching or repair-round counting. Operate only on explicitly selected repositories under the owner's control.

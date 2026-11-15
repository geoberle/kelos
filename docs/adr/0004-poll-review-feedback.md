# Poll review feedback and schedule Kelos work

Extend the existing Go component to poll review feedback and schedule Kelos Tasks, superseding ADR 0001's restriction on custom scheduling. Native Kelos polling cannot reliably trigger from arbitrary comments or retain feedback arriving during execution, and independent reviewers must be able to hand findings to the editor. Go owns feedback tracking and reliable task scheduling; Kelos executes the agent-managed fix/review loop without repair-round counting.

ADR 0005 refines execution ownership: OpenCodeReview reviews code, Kelos implements fixes, and Go coordinates the cycle.

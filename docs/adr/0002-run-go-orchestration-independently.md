# Run Go orchestration independently of Kelos

Run deterministic Go orchestration as a dedicated Kubernetes Deployment in the same cluster as Kelos. Kelos executes agent work; keeping deterministic reconciliation outside its task execution avoids coupling ordinary eligibility checks and GitHub mutations to an agent harness. Whether issue admission uses labels or direct Task creation remains a separate decision.

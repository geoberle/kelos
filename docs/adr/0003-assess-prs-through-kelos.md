# Assess pull requests through Kelos

Go submits commit-specific PR assessment Tasks to Kelos instead of calling an LLM API directly. Assessments need the full Codex harness with repository access, tools, skills, and agent capabilities, so reusing Kelos execution justifies asynchronous Task handling. Go validates the structured result and owns the resulting suggestion label and merge operation.

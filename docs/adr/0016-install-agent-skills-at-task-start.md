# Install agent skills at Task start

The implementation agent and the fix agent get skills from
`geoberle/skillshare-config` through `AgentConfig.spec.skills`. Kelos installs
them with the skills.sh installer in an init container when a Task starts, and
Codex finds them under their own names. The skills are `tdd`, `pr`,
`diagnosing-bugs`, `domain-modeling`, `codebase-design` and `implement`. The
assessment agent gets none: it reads and judges and does not write code.

A custom worker image was rejected because it must be built, published and
bumped on every skill change and every Kelos upgrade. Inlining the skill text
in the manifest was rejected because it needs a sync step.

The cost is that the skills are not pinned. A Task takes the default branch of
the skills repository as it is at that moment, and each Task start downloads
from GitHub and npm. A change to a skill therefore reaches the agents without a
deployment or a review in this system, and a Task fails to start when either
service is unreachable. The skills repository is public and owned by the same
person who approves merges here.

The skills are listed twice in the deployment, once per AgentConfig, because
the orchestrator references a single AgentConfig for the fix agent.

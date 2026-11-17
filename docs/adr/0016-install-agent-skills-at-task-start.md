# Install agent skills at Task start

The implementation agent and the editor get skills from
`geoberle/skillshare-config` through `AgentConfig.spec.skills`. Kelos installs
them with the skills.sh installer in an init container when a Task starts, and
Codex lists them under the names in their `SKILL.md`. The skills are `tdd`,
`pr`, `diagnosing-bugs`, `domain-modeling`, `codebase-design` and `implement`.
The assessment agent gets none of them: they guide writing code, and the
assessment agent writes none.

Baking the skills into the worker image `ghcr.io/hearthctl/agent` was rejected
because every skill change would need an image build and a new pin. Inlining
the skill text in the manifest was rejected because it needs a sync step and
makes the manifest large.

The cost is that nothing here is pinned. A Task takes the default branch of the
skills repository as it is when the Task starts, and the init container fetches
the installer from npm and git from the Alpine package mirror. A change to a
skill therefore reaches the agents without a deployment, and a Task fails to
start when GitHub, npm or the Alpine mirror is unreachable. This is an
exception to pinning every image and chart by digest.

`implement` tells the agent to finish with the `code-review` skill, which is
not installed.

The skills are listed twice in the deployment, once per AgentConfig, because
the orchestrator references a single AgentConfig for the editor.

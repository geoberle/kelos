# Bake agent skills into the worker image

The Codex agents get skills from `geoberle/skillshare-config` through the
worker image `ghcr.io/hearthctl/agent`. Its build downloads the repository at a
fixed commit and places `tdd`, `pr`, `diagnosing-bugs`, `domain-modeling`,
`codebase-design` and `implement` in Codex's skill directory. The deployment
pins the image by digest, so the skills are pinned like every other input.

The image is shared, so the assessment agent sees the skills too, although
they guide writing code and the assessment agent writes none.

Installing the skills at Task start through `AgentConfig.spec.skills` was
tried first and worked. It was rejected because nothing in it is pinned: a
Task takes the default branch of the skills repository as it is at that
moment, so a skill change reaches the agents without a deployment, and each
Task start needs GitHub, npm and the Alpine package mirror. Inlining the skill
text in the manifest was rejected because it needs a sync step and makes the
manifest large.

The cost is that a skill change needs a new image and a new pin.

`implement` tells the agent to finish with the `code-review` skill, which is
not installed.

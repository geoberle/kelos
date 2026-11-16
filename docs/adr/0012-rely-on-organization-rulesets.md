# Rely on organization rulesets

Branch protection comes from an organization ruleset that forbids pushing to
the default branch without a pull request. The controller reads the effective
rules of the target branch and no longer reads classic branch protection.

Before merging, the controller requires a `pull_request` rule on the target and
stops when the rules forbid merge commits. It does not inspect bypass lists or
other rules; GitHub enforces those when the merge is requested.

No required checks are configured in GitHub, because a ruleset names specific
checks and repositories differ. The controller instead requires every check
that ran on the commit to be green, counting skipped and neutral as green. A
repository without CI merges on review evidence and approval alone.

Editor branches are not protected and the controller does not check them.
The editor is instructed to use regular pushes only; nothing on GitHub prevents
a force push. This supersedes ADR 0009.

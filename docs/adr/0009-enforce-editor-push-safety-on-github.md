# Enforce editor push safety on GitHub

Editors push directly to their assigned branches. GitHub protection must prohibit
force pushes and deletion and apply to the editor identity without bypass.
Prompt instructions retain the exact-head check and prohibit automatic rebasing
or merging of concurrent work; they are not the enforcement mechanism against
history replacement. A rejected push preserves the editor's work for a human.

The operator made `geoberle/kelos` public to enable branch protection without a
plan change. Its native issue workflow creates `agent/issue-*` branches; the
`agent/**` protection rule prohibits force pushes and deletion with administrator
enforcement. Other editor branches require equivalent effective protection before
execution. Repository visibility alone does not provide protection, and editor
protection does not configure the separate required CI and merge gates on `main`.

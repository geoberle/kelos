# Verify through one make contract

Every repository exposes the same two make targets, and CI reports them as one
check named `verify`. This follows Prow, where the context name is fixed, the
pull request is tested merged on top of its base, and jobs call make targets,
and the `make verify` convention of Kubernetes and Kelos.

`make verify` runs everything that needs no extra infrastructure: formatting,
linters, unit tests and build. An agent must pass it in its pod before it opens
a pull request. `make e2e` is optional and holds what needs infrastructure the
pod lacks, such as a container runtime. Only CI runs it. This replaces
per-repository prose telling agents which checks to skip.

The shared part lives in `hearthctl/.github` as two composite actions, because
a reusable workflow would name the check after both jobs and leave the caller
no place to install its toolchain. `checkout` checks out the current tip of the
pull request's base branch and merges the pull request on top; a conflict fails
the check with a "needs rebase" message. `verify` runs `make verify` and then
`make e2e` when the Makefile defines it. Each repository keeps a caller workflow
whose `verify` job installs the toolchain between the two.

Tests run with the race detector where the host supports it. The race runtime
cannot start on a Raspberry Pi 5, so there the same target runs them without
it and CI supplies the race run.

The fixed name allows the organization ruleset to require `verify`, which
ADR 0012 ruled out while check names differed. The controller's rule that every
check on the commit is green stays in force.

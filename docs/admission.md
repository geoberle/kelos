# Issue admission deployment

The orchestrator polls only repositories in its v1 JSON configuration. It examines
issues (not PRs), follows issue and blocker pagination, and adds `agent-start` to
open `ready-for-agent` issues only when every returned GitHub blocker is closed.
GitHub dependency API failures prevent admission and appear in structured logr
error logs. A failed repository does not prevent polling other repositories.
The credential needs Issues read/write access in the allowlist and read access to
all blocking issues; inaccessible dependency evidence must not be treated as clear.

On each poll, readiness removal, issue closure, or an open blocker causes removal
of `agent-start`. Withdrawal is eventual, not atomic with Kelos discovery. Kelos
can already have discovered an issue before withdrawal. This component does not
cancel or delete Tasks, including queued Tasks; stop the spawner and intervene in
Kelos when immediate cancellation is required. A stricter guarantee needs a
coordinated execution gate and is not implemented by this label-only slice.

Repeated polls and process restarts consult GitHub labels rather than in-memory
admission history. Kelos owns Task creation and deduplication. Retain its Tasks;
removing a Task must not serve as an implicit retry mechanism. This slice neither
calls the Kubernetes API nor needs a Role/RoleBinding. Kubernetes Task behavior is
not tested by the admission HTTP fixtures and remains an integration acceptance
check.

## Configuration and rollout

Example configuration (the repository is explicitly selected by the operator):

```json
{"version":"v1","repositories":["hearthctl/kelos-orchestrator"],"pollInterval":"1m"}
```

Run `orchestrator --config-file=config.json --app-credential-dir=/credential`.
Validation precedes reading credentials or constructing the GitHub client.
The orchestrator authenticates as a GitHub App installation and renews its
tokens itself; the app key is read at startup, so restart the Deployment after
rotating it. The Helm chart references an existing Secret named by
`credentialSecret`, with `appID`, `installationID` and `privateKey` keys. Do not put credentials in values or source control. Configure
`imagePullSecrets` for private GHCR access. No Service or inbound listener exists.

`make fmt`, `make fmt-check`, `make lint`, `make lint-fix`, `make test`, `make build`,
`make image`, and `make chart` provide local equivalents of CI. Helm 3.19.2 and a
Docker Buildx-capable daemon are required for packaging checks. Go formatter and
linter versions are pinned in Makefile.

A `vMAJOR.MINOR.PATCH` tag triggers multiarchitecture image publication to
`ghcr.io/<repository-owner>/kelos-orchestrator` and OCI chart publication to
`oci://ghcr.io/<repository-owner>/charts/orchestrator`. The packaged chart embeds
the image manifest digest and matching release version; the source chart's 0.1.0
image tag is a development placeholder, not evidence that a release exists.
The workflow pulls the published chart and renders it. Package visibility is not
changed. Operators must establish registry access before installing private
artifacts; anonymous pulls are not assumed.

Install/upgrade using the published chart version and explicit repository values.
Use Helm rollback to restore a previous chart/image digest. Publication does not
update Hearth pins or deploy to Hermine. Those changes belong to the integration
slice. Native target execution, authenticated registry pull, live GitHub mutation,
and Kelos Task deduplication require separate evidence before acceptance.

Admission is decided at native Kelos discovery. An already-created Task continues after eligibility changes; no pre-start recheck or automatic cancellation is required.

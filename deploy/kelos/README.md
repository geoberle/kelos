# Native implementation execution

This configuration implements issue #3's deployment slice. It is not a completed
live deployment. Only `geoberle/kelos` is configured; keep the Go admission
allowlist identical. The spawner starts suspended. No Go runtime changes are made.

## Pins and setup

Use the external upstream chart at commit
`0f7b318ab66eee7f22079dbf0796dcb384ca9698`, path
`internal/manifests/charts/kelos`. Do not vendor it. Its source Chart.yaml version
is 0.1.0, which alone does not identify this snapshot. Check out and verify the
commit before rendering. A published chart/image release corresponding to this
source has **not** been verified. `RELEASE_PIN_REQUIRED` is deliberately not a
working image tag: supply a verified immutable upstream release tag with
`--set-string image.tag=...` before installing. Publication/access and target
architecture checks remain outstanding; do not substitute latest or change
package visibility. Hearth integration owns final artifact pins.

Create `kelos-system` for the controller and `kelos-agents` for the resources.
Provision Secrets in `kelos-agents` through the deployment's secret manager:

- `kelos-github`: PAT key `token`, or GitHub App keys `appID`, `installationID`,
  `privateKey` per upstream Workspace documentation. Restrict to the configured
  repository; grant contents and pull requests write, issues write for enrollment
  and status comments, and metadata read. Validate actual permissions in a test
  repository. Use an identity whose pushes trigger Actions, not an Actions job's
  `GITHUB_TOKEN`. Record its login for exclusion from human-feedback processing.
- `kelos-openai`: key `CODEX_API_KEY`. Confirm access to the configured Codex
  model before activation. Never put credential values in Helm values or git.

Initial values: one editor, 60-second discovery, Codex gpt-5.4/high, worker
requests 1 CPU/2Gi and limits 4 CPU/8Gi. These are starting settings requiring
cluster capacity and model-access validation. Workspace is a per-Task checkout;
there is no durable PVC configured. Preserve failed worker Pods/logs and recover
unpublished changes before deleting Jobs or Tasks.

Render and lint without deploying:

```sh
UPSTREAM=/tmp/kelos-upstream-inspect
[ "$(git -C "$UPSTREAM" rev-parse HEAD)" = 0f7b318ab66eee7f22079dbf0796dcb384ca9698 ]
helm lint "$UPSTREAM/internal/manifests/charts/kelos" -f deploy/kelos/values.yaml
helm template kelos "$UPSTREAM/internal/manifests/charts/kelos" \
  --namespace kelos-system -f deploy/kelos/values.yaml > /tmp/kelos.yaml
```

After installing the pinned chart and CRDs through Hearth, validate custom
resources with `kubectl apply --dry-run=server -f deploy/kelos/resources.yaml`.
Apply resources while suspended, verify Secrets and repository labels
(`ready-for-agent`, `agent-start`, `agent-managed`), then explicitly activate with
`kubectl -n kelos-agents patch taskspawner geoberle-kelos-implementation --type=merge -p '{"spec":{"suspend":false}}'`.
No inbound endpoint is configured. Allow outbound DNS, Kubernetes API, GitHub
API/git, image registries, model API and repository build dependencies.

## Retention and intervention

Never set `ttlSecondsAfterFinished`, including zero. The pinned API explicitly
states that deleting finished Tasks permits discovery to create work again.
Retain successful and failed Tasks, their identities and the TaskSpawner across
restarts; do not rename/recreate the spawner as a rollout strategy. Back up CRs
and status with cluster state. Labels and status comments do not replace Task
deduplication evidence. Removing a Task is an intentional manual retry, only
after suspending discovery, preserving work and checking for an existing PR.
Failed executions are not automatically retried by this configuration.

Suspend stops new discovery, not existing Tasks. Withdrawing `agent-start` also
cannot cancel a previously discovered Task. Native GitHub filters are evaluated
at discovery, not immediately before execution. This is an unresolved admission
race, not a cancellation guarantee.

Execution-time options for the parent to decide:

1. A deterministic preflight in `worker.podOverrides.extraInitContainers` can
   re-read GitHub eligibility before starting the harness. It requires an approved
   check executable/image, credentials and failure/retry semantics; labels alone
   still lag blocker changes. No such check is installed here.
2. An upstream controller execution gate could perform the same check before
   launching the Job, but requires upstream changes and still has a post-check
   race with mutable GitHub state.

An agent prompt recheck is not a deterministic admission gate and is not used
as a substitute. Resolve this policy before unattended activation.

## Integration acceptance procedure (not yet executed)

In an isolated repository/cluster, update both explicit repository settings and
the Go allowlist together. Record Task names/UIDs and GitHub evidence:

| Scenario | Required observable result |
| --- | --- |
| Open admitted issue | One Task; linked PR carries agent-managed; status comment updates |
| Ready but not admitted issue | No Task |
| Repeated polls, then spawner/controller restart | Same Task identity; no second PR |
| Failed Task, repeated polls and restart | Failure visible; retained Task prevents rediscovery |
| Remove agent-start after discovery | Existing Task remains; demonstrates documented limitation |
| Push with worker credential | Intended OCR workflow starts on ARC |

Capture Task YAML/status, logs, PR URL, enrollment label and workflow run ID.
Check Kubernetes API rejection of invalid references/schema separately from
runtime missing-Secret/image-pull failures. Live credentials, API validation,
repeat-poll/restart behavior, LLM implementation, PR enrollment, outbound network
operation, OCR triggering and recovery have not been demonstrated. Helm rendering
is not evidence for these acceptance criteria.

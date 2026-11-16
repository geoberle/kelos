# Native Kelos execution prerequisites

This configuration supplies native execution prerequisites for issues #3/#6/#9. It is not a completed
live deployment. Only `hearthctl/kelos-orchestrator` is configured; keep the Go admission
allowlist identical. The spawner starts suspended. No Go runtime changes are made.

## Pins and setup

Use official [Kelos v0.64.0](https://github.com/kelos-dev/kelos/releases/tag/v0.64.0),
source commit `83a7405860df50e91e3313eb63b8e22f244fd943`, with OCI chart
`oci://ghcr.io/kelos-dev/charts/kelos`, version `0.64.0` (appVersion `v0.64.0`).
Its OCI manifest digest is
`sha256:6abd9dd549bde51c07ed9aecc5356756685e87aad6208562d44bad23ed59a8eb`.
The [release workflow](https://github.com/kelos-dev/kelos/blob/83a7405860df50e91e3313eb63b8e22f244fd943/.github/workflows/release.yaml)
packages that chart and publishes release-tagged images. Do not vendor the chart.

On 2026-11-15, anonymous GHCR manifest requests for `v0.64.0` verified the
controller, Codex, spawner, worker-runner and session-runtime index digests now
recorded in values.yaml. Each index contains Linux amd64 and arm64 images
(alongside attestation entries). Digest references, not mutable tags alone,
select these artifacts. The empty shared image tag prevents Helm appending a
suffix to the controller digest; the released controller preserves explicitly
tagged/digested managed image references. Other harnesses are not configured.
No image was built or published and no registry visibility was changed.

The earlier research snapshot `0f7b318ab66eee7f22079dbf0796dcb384ca9698`
contains newer APIs, but this Workspace and TaskSpawner use only fields present
in v0.64.0. Both passed offline validation against the packaged chart's
v1alpha2 OpenAPI schemas, with unknown spec fields rejected. Helm lint and
render also passed. This does not execute CEL or admission webhooks.

Install cert-manager with its CRDs, controller, webhook and CA injector before
Kelos. The chart unconditionally creates a cert-manager.io/v1 Issuer and
Certificate for the controller webhook and uses CA injection for conversion
and validation. Disabling event webhook servers does not remove this dependency.
`crds.install: true` installs all 11 bundled Kelos CRDs, including Workspace,
TaskSpawner and Task; retain the entire release-matched set. Keep the internal
controller webhook reachable from the Kubernetes API server. No public ingress
is required. Target-cluster cert-manager compatibility and server-side admission
remain live acceptance checks.

Create `kelos-system` for the controller and `kelos-agents` for the resources.
Provision Secrets in `kelos-agents` through the deployment's secret manager:

- `kelos-github`: PAT key `GITHUB_TOKEN`, or GitHub App keys `appID`, `installationID`,
  `privateKey` per upstream Workspace documentation. Restrict to the configured
  repository; grant contents and pull requests write, issues write for enrollment
  and status comments, and metadata read. Validate actual permissions in a test
  repository. Use an identity whose pushes trigger Actions, not an Actions job's
  `GITHUB_TOKEN`. Record its login for exclusion from human-feedback processing.
- `kelos-openai`: key `CODEX_AUTH_JSON`, the `auth.json` of a Codex ChatGPT
  login used by no other client, labeled `kelos.dev/codex-oauth-refresh=true` so
  Kelos keeps it refreshed. Confirm access to the configured Codex model before
  activation. Never put credential values in Helm values or git.

Implementation spawner values: one concurrent implementation Task, 60-second discovery, Codex gpt-6.1-sol/high, worker
requests 1 CPU/2Gi and limits 4 CPU/8Gi. These are starting settings requiring
cluster capacity and model-access validation. Workspace is a per-Task checkout;
there is no durable PVC configured. Preserve failed worker Pods/logs and recover
unpublished changes before deleting Jobs or Tasks.

Render and lint without deploying:

```sh
CHART_DIR=$(mktemp -d)
helm pull oci://ghcr.io/kelos-dev/charts/kelos --version 0.64.0 \
  --untar --untardir "$CHART_DIR"
# Require the pull output digest to equal:
# sha256:6abd9dd549bde51c07ed9aecc5356756685e87aad6208562d44bad23ed59a8eb
helm lint "$CHART_DIR/kelos" -f deploy/kelos/values.yaml
helm template kelos "$CHART_DIR/kelos" \
  --namespace kelos-system -f deploy/kelos/values.yaml > /tmp/kelos.yaml
```

After installing the pinned chart and CRDs through Hearth, validate custom
resources with `kubectl apply --dry-run=server -f deploy/kelos/resources.yaml`.
Apply resources while suspended, verify Secrets and repository labels
(`ready-for-agent`, `agent-start`, `agent-managed`), then explicitly activate with
`kubectl -n kelos-agents patch taskspawner hearthctl-kelos-orchestrator-implementation --type=merge -p '{"spec":{"suspend":false}}'`.
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
at discovery, not immediately before execution. Admission is decided at native Kelos discovery. The operator accepts that an
already-created Task continues if readiness or blockers subsequently change.
Go withdraws the label on the next successful poll to prevent later discovery.
No execution-time preflight or automatic cancellation is required.

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

## Go-created editor and assessment Tasks

All references below are in `kelos-agents`. Merge this fragment into the Go
orchestrator chart values only when execution is intended; configuring these
sections enables scheduling independently of the suspended implementation spawner.
Replace the controller login with the actual dedicated controller identity.

```yaml
configuration:
  repositories: [hearthctl/kelos-orchestrator]
  controllerLogin: REPLACE_WITH_DEDICATED_CONTROLLER_LOGIN
  mergeEnabled: false
  editor:
    namespace: kelos-agents
    workspaces:
      hearthctl/kelos-orchestrator: hearthctl-kelos-orchestrator
    agentConfig: kelos-editor
    secret: kelos-openai
  assessment:
    namespace: kelos-agents
    workspaces:
      hearthctl/kelos-orchestrator: hearthctl-kelos-orchestrator-assessment
    agentConfig: kelos-assessment
    secret: kelos-openai
```

`secret` is the Codex OAuth Secret, not the GitHub credential. Provision the
separate `kelos-github-assessment` Secret with key `GITHUB_TOKEN`: a
repository-scoped credential with contents, pull requests and metadata read
only, and no write permissions. The Workspace has no read-only switch; GitHub
permissions enforce remote read-only access. Local checkout writes needed for
checks are allowed. Never substitute the editor or controller credential.
Keep controller credentials out of worker Pods. Verify effective permissions
with the real credential before activation; prompt instructions are not a
security boundary.

Both Go Task constructors select Codex with API-key authentication and reference
these AgentConfigs. Their prompts own assignment and result contracts; the
AgentConfigs intentionally add no competing output format. See
[editor execution](../../docs/editor-status.md) and
[assessment execution](../../docs/assessment.md) for result validation and gates.

Unlike the implementation TaskSpawner template, these Go constructors omit
model, effort and pod resource overrides. In pinned v0.64.0, the Codex entrypoint
passes model and effort only when supplied, leaving Codex's own defaults;
the Job builder supplies no worker requests or limits without overrides
(cluster admission such as LimitRange may supply them). AgentConfig cannot set
these fields. Model access, effective runtime defaults and capacity remain an
explicit operational consideration; the implementation gpt-6.1-sol/high and
1 CPU/2Gi requests do not carry over to Go-created Tasks.

Before enabling scheduling, install these resources and Secrets, run one Go
controller with its Task read/create RBAC, verify administrator-enforced classic
branch protection forbids force pushes and deletion, and provide working OCR
on ARC for editor pushes. Permit the outbound endpoints listed above and ensure
the pinned worker image can run repository checks. Keep Tasks and recoverable
worker evidence; no TTL is introduced.

Remaining live acceptance: server-side CR admission and reference resolution,
Secret permissions (including denied assessment writes), image pulls, Codex
authentication and effective defaults, editor result capture and protected push
triggering OCR, assessment response capture at the assigned SHA, repeated-poll
and restart retention, and the complete review/assessment/merge gates. Offline
schema and Helm checks do not demonstrate any of these. No workflow was
activated or deployed by this resource change.

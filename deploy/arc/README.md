# OCR runner example

Render/install order: `gha-runner-scale-set-controller` release `arc-controller`
in namespace `arc-systems`, then `gha-runner-scale-set` release `kelos-ocr` in
`arc-runners`. Both charts are version **0.13.1** from
`oci://ghcr.io/actions/actions-runner-controller-charts`. Values here rendered
successfully against those upstream charts. This example is not a deployment.

The controller chart OCI digest is
`sha256:3a7becceb2c8f5e400a6b828390f43d782bb8e9f58aaeade536c899570bcc572`;
the runner chart digest is
`sha256:39f9b61ee7e2865d7b8dd0e4e28b7c1a065765fc2ce5bf90874dd8e8be8ee2b2`.
Controller and runner values pin verified OCI index digests. Both provide Linux
amd64 and arm64 images (verified 2026-11-16); target-cluster startup remains a live
acceptance check. No Docker daemon, host mounts, ingress or
privileged containers are needed. Runners are ephemeral and have no mounted
Kubernetes API token. Do not share this scale set with untrusted build jobs.
Restrict repository workflow changes and runner access to trusted maintainers.

Provision `arc-github-pat` in `arc-runners` outside Git, using ARC's supported
`github_token` Secret key. The operator selected a classic personal access token
with `repo` scope for repository runner registration. Track its expiry and rotate
the Secret before expiration. Hearth reads it from ignored `arc_github_token`.
Provision Actions secrets `OCR_LLM_URL` and `OCR_LLM_AUTH_TOKEN` and variable
`OCR_LLM_MODEL`. OCR publishes using the job's short-lived GitHub token.


Allow outbound DNS/HTTPS to GitHub Actions/API/artifact endpoints, GHCR, npm and
the selected model endpoint. No inbound internet connection is required. Apply
cluster-specific egress restrictions and deny runner access to unrelated
cluster services/metadata before deployment; these values do not implement
network isolation. Hearth's configuration/state separation and pinned asset
conventions were inspected read-only; Hearth integration belongs to issue #10.

Local validation (Helm 3 and actionlint):

```sh
helm template arc-controller oci://ghcr.io/actions/actions-runner-controller-charts/gha-runner-scale-set-controller --version 0.13.1 --namespace arc-systems -f deploy/arc/controller-values.yaml
helm template kelos-ocr oci://ghcr.io/actions/actions-runner-controller-charts/gha-runner-scale-set --version 0.13.1 --namespace arc-runners -f deploy/arc/runner-values.yaml
actionlint -ignore 'label "kelos-ocr" is unknown' .github/workflows/ocr.yml
python3 -m json.tool .opencodereview/rule.json >/dev/null
```

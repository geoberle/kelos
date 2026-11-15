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
Runner release tag v2.331.0 exists upstream; image pull and architecture support
remain live acceptance checks. No Docker daemon, host mounts, ingress or
privileged containers are needed. Runners are ephemeral and have no mounted
Kubernetes API token. Do not share this scale set with untrusted build jobs.
Restrict repository workflow changes and runner access to trusted maintainers.

Provision `arc-github-app` in `arc-runners` outside Git, using ARC's supported
GitHub App Secret keys `github_app_id`, `github_app_installation_id`, and
`github_app_private_key`. Provision Actions secrets `OCR_LLM_URL` and
`OCR_LLM_AUTH_TOKEN` and variable `OCR_LLM_MODEL`. No credential values are
provided. Grant the App only the permissions ARC requires for the selected
repository. OCR publishes using the job's short-lived GitHub token.

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

# OCR acceptance on Hermine

## Read-only preflight, 2026-11-16

Validated from branch `ocr-arc-acceptance`, initially at
`ace308b6007b9ba282839dc5505977ae2864265d`. No automation was activated.

- `kubectl --kubeconfig ~/.kube/hermine --request-timeout=10s get nodes`:
  Hermine Ready, Kubernetes v1.35.5+k3s1, Linux arm64, no external IP.
- cert-manager controller, cainjector and webhook: each 1/1 available.
- Namespaces `arc-systems` and `arc-runners`: NotFound. No ARC Secret or
  runner deployment exists in those intended namespaces.
- GitHub `repos/geoberle/kelos/actions/runners`: `total_count: 0`.
- Repository is public, default branch `main`. Registered workflows list only
  Admission CI; OCR is not yet installed on the default branch.
- `gh secret list --repo geoberle/kelos` and `gh variable list --repo
  geoberle/kelos` both returned empty lists. Required inputs are
  `OCR_LLM_URL`, `OCR_LLM_AUTH_TOKEN` and `OCR_LLM_MODEL`.
- `/tmp/kelos-hearth-integration/kelos/deploy/values.local.yaml` is absent.
  The operator subsequently selected PAT authentication; supply `arc_github_token`
  through Hearth's ignored local credential input. Supply the separate implementation App/PAT credential
  there as well. The available interactive gh login does not establish that the
  eventual implementation identity has the required permissions or triggers.

The missing model input is an OpenAI-compatible endpoint, credential and model
accessible over public IPv4 HTTPS under the prepared runner policy. A private
endpoint requires a narrow policy allowance. Neither provider access nor intended
implementation/App credentials can be verified from absent inputs. Do not print
credentials while checking presence or access. Public source visibility does not
change GHCR package visibility or prove authenticated package pulls.

## Deterministic verification

Use the pinned local chart archives (0.13.1) and Helm binary:

```sh
/tmp/kelos-helm/darwin-arm64/helm template arc-controller /tmp/gha-runner-scale-set-controller-0.13.1.tgz --namespace arc-systems -f deploy/arc/controller-values.yaml >/tmp/kelos-ocr-controller-render.yaml
/tmp/kelos-helm/darwin-arm64/helm template kelos-ocr /tmp/gha-runner-scale-set-0.13.1.tgz --namespace arc-runners -f deploy/arc/runner-values.yaml >/tmp/kelos-ocr-runner-render.yaml
GOTOOLCHAIN=go1.25.5 go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.7 -ignore 'label "kelos-ocr" is unknown' .github/workflows/ocr.yml
python3 -m json.tool .opencodereview/rule.json >/dev/null
node --test /tmp/kelos-ocr-upstream/scripts/github-actions/post-review-comments.test.js
GOTOOLCHAIN=go1.25.5 make fmt test lint fmt-check
```

Both renders, actionlint, rule JSON validation and the upstream publication test
passed. `GOTOOLCHAIN=go1.25.5 make fmt test lint fmt-check` passed, including
race-enabled admission, editor, merge and review tests; formatting changed no code. Upstream
checkout HEAD was verified as `7e5ce06c88de5ce81df5dc4f24e2e485b517fae8`.
The source contract is documented in [the handoff](opencodereview-handoff.md):
critical/high mean consequence-based must-fix; medium/low are advisory;
selected coverage is distinct from excluded coverage; publication failures block
completion; reruns have distinct attempts; thread resolution is deliberately off.
The upstream publication test exercises its GitHub boundary without publishing.
It does not demonstrate a live model applying the severity policy.

## Live acceptance after parent integration and explicit rollout

Do not execute this procedure until the parent integrates the final runtime and
authorizes deployment. Use Hearth PR https://github.com/geoberle/hearth/pull/1
and its release/credential procedure; do not modify the unrelated dirty Hearth
checkout. Preserve all component pins and keep merging and issue spawning disabled
until the isolated smoke scenario is ready.

1. Provision the missing credentials and model settings. Install trusted OCR
   workflow/rules on the base branch. For a different test repository, update
   workflow guard, runner URL and orchestrator allowlist together. Deploy through
   Hearth, then record controller availability, runner registration, image digest
   and architecture, and successful Node/npm setup in the actual Action log.
2. Verify k3s network-policy enforcement. The prepared Hearth policy selects
   `kelos.dev/review-runner=true`, denies ingress and allows CoreDNS plus public
   IPv4 TCP/443, excluding private/loopback/link-local addresses. Check that the
   actual runner Pod has this label and no service-account token, host mounts or
   privileged container. From that runner network namespace, probe GitHub,
   artifact storage, npm and the model endpoint successfully; probe Kubernetes
   service IP, unrelated LAN services and metadata unsuccessfully. Record exit
   status and destinations without auth headers. An absent external IP alone
   does not prove isolation. The example values in this repository alone do not
   install this policy; use the prepared Hearth values and policy together.
3. Using the intended implementation App/PAT, create an enrolled PR with a
   demonstrable Go defect and changed `_test.go`, `testdata/` and `.golden` files.
   Record actor, PR, head and triggered run. Push a repair using the same identity
   and verify a new synchronize-triggered run. GITHUB_TOKEN-produced events are
   unsuitable for proving these triggers. Do not substitute the interactive gh
   identity for the intended implementation credential.
4. Download each run's `ocr-review-result-<run>-<attempt>`,
   `kelos-ocr-identity-<run>-<attempt>` and
   `kelos-ocr-publication-<run>-<attempt>` artifacts with `gh run download`.
   Retain sanitized captures and run URLs. Verify PR/head/attempt identity and
   merge-base range, selected/completed test and golden paths, exclusions,
   severity, inline review commit and sticky summary. Retain unplaceable findings
   in JSON and verify failed publication cannot authorize completion.
5. Rerun via Actions UI or `gh run rerun <run>` only when authorized. Record new
   attempt and distinct artifacts. Confirm a clean repair reviews the new head;
   stale evidence does not count. Inspect old and duplicate threads: with
   `resolve_outdated: false`, human resolution remains required even after a
   clean review. Editors must only reply, never resolve.
6. Capture complete, partial, failed and skipped manifests in the isolated test
   setup (a constrained review budget can produce partial coverage; a change
   with no selected files can produce skipped coverage). Record actual outcomes,
   never assume a stimulus guarantees a state. Exercise missing artifacts and
   publication failure separately. Sanitize captures before committing fixtures.
   Compare controller results to the existing HTTP fixture cases: only complete,
   acceptable current coverage with no must-fix or pending human feedback permits
   review completion. Partial/failed need manual rerun; skipped needs human LGTM.

Until these steps have evidence, issue #4's actual runner/model/publication and
credential-trigger acceptance remains incomplete. Local deterministic checks are
complete independently of those missing live inputs.

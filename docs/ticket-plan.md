# Published implementation slices

Source: https://github.com/hearthctl/kelos-orchestrator/issues/1

Approved and published as sub-issues of specification #1. Section numbers are planning identifiers; use the GitHub links below for issue numbers. Each implementation slice includes relevant table-driven tests, golden fixtures, restart/deduplication behavior, and failure handling at the agreed external test boundary. Shared Go runtime/configuration is introduced by slice 1; later slices extend the same binary. No separate generic framework or final hardening phase is proposed.

## Concrete delivery contract

- **Go executable and container:** build the orchestration binary and publish a versioned OCI container image. Include linux/amd64 and linux/arm64 support for Hearth deployment portability; verify the selected target architecture actually runs. Release automation produces identifiable, pinned artifacts, not only local binaries.
- **OCI Helm chart:** package and publish a versioned Helm chart as an OCI registry artifact (a chart, not a runnable container image). It installs the Go component with repository/agent configuration, Deployment, service account/RBAC, resource settings and references to pre-existing credential Secrets. Keep upstream Kelos and ARC charts external and pinned; integration supplies their configuration rather than vendoring their sources.
- **Tooling and options:** follow ARO-HCP templatize's RawOptions → ValidatedOptions → Options flow. Flags bind raw input; Validate checks it; Complete resolves runtime dependencies; execution consumes completed Options. Use private wrapper types as in the reference and separate command wiring from options/runtime behavior. Provide pinned lint, lint-fix and fmt tools, local commands and equivalent CI enforcement, plus test/build/image/chart checks. Transfer relevant ARO conventions without importing unrelated generator-specific behavior or ARO-only tooling dependencies.
- **Templatize lint/fmt baseline:** golangci-lint v2 configuration and its errcheck, forbidigo, govet, importas, ineffassign, staticcheck and unused checks; goimports with this module's local import prefix; applicable gci/gofmt and import-group conventions. Select compatible pinned versions at implementation time. Preserve logr logging conventions. Formatting CI checks cleanliness rather than silently modifying committed code.
- **Hearth integration:** changes in the Hearth repository consume published artifacts with explicit chart/image pins, deployment values, Secret provisioning references, and documented rollout/recovery. They are a delivered change set, not an instruction to manually copy example YAML. Target Hermine/k3s using Hearth's existing Ansible/Helm application deployment conventions.
- **Publication versus deployment:** building/publishing does not automatically mutate Hearth deployment pins or deploy. Registry names, artifact visibility and access must be explicit; Hearth's current Forgekeeper example assumes public anonymous GHCR pulls, while this source repository is private. Do not make packages public without a decision.

Reference inspection: ARO-HCP's templatize commands use RawOptions.Validate → ValidatedOptions.Complete → completed Options execution. Its lint/fmt targets are repository-level, not module-local. Hearth installs external OCI charts through Ansible/Helm on Hermine/k3s, the selected target. The newer Talos hearth cluster is outside this rollout's scope.

## 1. Admit eligible issues through the deployed Go component

**Blocked by:** None.

**Delivers:** An outbound-only Go Deployment polls configured repositories and adds `agent-start` only to ready issues whose blockers are fulfilled.

- Ship the initial Go binary/container image, OCI Helm chart and release pipeline described in the delivery contract, with matching deployable pins and verified chart pull/render behavior.
- Establish templatize-style options and pinned lint/fmt/test CI from the first runnable slice. Invalid raw input fails before runtime dependencies are initialized.

- Explicit repository allowlist and versioned configuration; credentials referenced from Secrets.
- Deterministic readiness/blocker decisions, repeated-poll idempotency, and visible API failures.
- Define behavior when readiness or blockers change before execution; surface any required change to the agreed interaction.
- Verify an eligible issue receives the label and an ineligible issue does not.

## 2. Turn admitted issues into agent-managed PRs

**Blocked by:** 1.

**Delivers:** Kelos's native issue spawner executes an admitted issue and creates an `agent-managed` PR linked to it.

- Deploy/configure Kelos prerequisites, worker credentials, workspace and implementation instructions.
- Demonstrate Task creation, implementation, PR creation/enrollment, and status reporting without inbound connectivity.
- Preserve native deduplication through the chosen Task retention policy; test repeated polling/restarts.
- Specify initial operational values and human setup steps rather than assuming credentials or infrastructure exist.

## 3. Review a PR with OCR on internal ARC runners

**Blocked by:** None.

**Delivers:** A test PR receives OCR findings and a commit-bound JSON artifact from an internal Kubernetes runner.

- Configure ARC and the OCR GitHub Action with outbound-only communication and pinned versions.
- Combine Steve/David instructions; establish an unambiguous machine representation of must-fix versus advisory findings.
- Verify relevant tests/golden fixtures are included in review coverage.
- Demonstrate actual artifact schema, incomplete/skipped states, inline publication, rerun behavior, and old-thread resolution behavior.
- Verify intended agent credentials can trigger review on PR creation and pushes; document manual thread resolution if OCR does not perform it.

## 4. Publish review completion from verified OCR evidence

**Blocked by:** 1, 3.

**Delivers:** Go consumes trusted OCR artifacts and publishes `review-done` for a complete clean current-commit result.

- Correlate workflow run/attempt, PR and reviewed head; ignore stale results and repeated observations.
- Must-fix findings prevent completion; advisories do not. Missing/malformed/partial/failed/skipped results do not grant completion.
- Publish concise failure explanations; failed/partial runs await manual rerun and skipped runs require human approval.
- Establish durable review evidence and invalidation on new commits, without a database.

## 5. Repair OCR findings and obtain a fresh review

**Blocked by:** 2, 4.

**Delivers:** Go hands current OCR findings to a Kelos editor, the editor pushes corrections and replies, and OCR reviews the new commit.

- One editor per PR; separate PRs progress independently. Persist assigned finding identities and explicit dispositions.
- Return fixed/already-satisfied/needs-human outcomes with reasons; missing outcomes remain pending.
- Reply in original inline threads or reference source conversation comments; never resolve review threads automatically.
- Failed/conflicted editors stop for intervention, preserve work, and do not mark feedback handled.
- Detect concurrent human pushes and prevent overwrite or automatic reconciliation.
- Demonstrate restart-safe Task creation/result consumption and a complete review/fix/review cycle without round counting.

## 6. Preserve and act on human feedback during automation

**Blocked by:** 5.

**Delivers:** An authorized human comment starts follow-up editing, including feedback received while another Task is running.

- Allow `geoberle` initially; exclude status/assessment bot messages and duplicate consumption of OCR-published findings.
- Maintain feedback identity and handling evidence through restarts; no quiet-period batching or command requirement.
- Invalidate review completion and approval for actionable feedback, even without a new commit.
- Retain later feedback for subsequent work, and block completion while feedback needs human attention.
- Define and demonstrate explicit recovery after stopped editors without automatically retrying them.

## 7. Merge exactly the commit approved by a human

**Blocked by:** 1.

**Delivers:** Human `lgtm` can cause a real merge commit only when every technical gate passes, with approval bound to the intended head.

- First verify approval/head recovery from GitHub evidence, including pushes during controller downtime. If the label interaction cannot be made reliable, stop and present a concrete alternative before implementation.
- Enforce required green CI, non-draft state, no hold, and no unresolved review threads; human approval does not need review-done.
- Remove stale approval; reject unauthorized or unbound approval and prevent approval reuse after new feedback or commits.
- Use direct merge with approved-head precondition; validate remaining label/feedback/hold races and required repository enforcement instead of claiming atomicity without evidence.
- Demonstrate positive and negative merge cases in an isolated repository plus deterministic fixture tests.

## 8. Assess unattended merge suitability through Kelos

**Blocked by:** 4, 7.

**Delivers:** A clean, CI-passing non-draft PR receives a Kelos assessment and either a concise needs-human explanation or commit-bound `lgtm` consumed by the shared merge path.

- Create one correlated assessment Task, consume validated commit/recommendation/reason JSON, and maintain one assessment comment.
- Use Codex investigation tools while leaving PR branch and GitHub state untouched by the assessor.
- Reject invalid/stale output; recover correlation after restarts without repeated model requests.
- New commits or authorized `reassess <full SHA>` comments permit reassessment; an unfavorable answer is not automatically retried.
- Failed executions and malformed results require an authorized `reassess <full SHA>` comment targeting the current head. Persist request identity and perform no automatic retries on unchanged commits.

## 9. Integrate published components into Hearth

**Blocked by:** 2, 3.

**Delivers:** A reviewable change set in Hearth installs the Go component, Kelos and ARC from pinned artifacts using the selected cluster's conventions.

- Commit component deployment values and artifact pins plus documented Secret inputs and rollout entry points. Consume the OCI chart/container published by slice 1 and upstream Kelos/ARC charts; do not duplicate source projects.
- Deploy to Hermine/k3s following Hearth's existing Ansible/Helm external-application conventions. Add component deployment pins/values, Secret provisioning, and a standalone rollout playbook; leave the Talos cluster unchanged.
- Verify registry access for the selected public/private artifact policy and support the target node architecture.
- Keep actual credentials out of tracked files, Helm values and logs; follow Hearth's ignored local credential input and Secret-provisioning conventions where applicable.
- Pass Hearth YAML lint, applicable rollout syntax checks, and Helm rendering with committed values; document install, update and rollback.
- Keep package publication independent from deployment; later behavior slices consume the same deployment contract through updated pins.

## 10. Verify the complete workflow and operating procedure

**Blocked by:** 6, 8, 9.

**Delivers:** A reproducible isolated-cluster demonstration from issue admission through review, repairs, assessment and guarded merge, with documented operator recovery.

- Run Kelos, Go and ARC/OCR together using real outbound-only connections and a test repository.
- Exercise human comments during editing, failed/skipped review, needs-human assessment, manual approval, hold, unresolved threads, stale results and concurrent pushes.
- Confirm all components agree on approval and feedback invalidation, durable evidence, credential identities and actual workflow triggers.
- Verify evidence retention/cleanup does not reopen completed work or erase approval provenance.
- Include configuration, credential provisioning and manual recovery instructions, and record remaining limitations explicitly.
- Exercise the published container and OCI chart through the Hearth integration, not an unrelated ad-hoc deployment. Update final deployment pins to the verified release.

## Published issues

- Slice 1: [Admit eligible issues through the deployed Go component](https://github.com/hearthctl/kelos-orchestrator/issues/2) — blocked by none.
- Slice 2: [Turn admitted issues into agent-managed PRs](https://github.com/hearthctl/kelos-orchestrator/issues/3) — blocked by #2.
- Slice 3: [Review a PR with OCR on internal ARC runners](https://github.com/hearthctl/kelos-orchestrator/issues/4) — blocked by none.
- Slice 4: [Publish review completion from verified OCR evidence](https://github.com/hearthctl/kelos-orchestrator/issues/5) — blocked by #2, #4.
- Slice 5: [Repair OCR findings and obtain a fresh review](https://github.com/hearthctl/kelos-orchestrator/issues/6) — blocked by #3, #5.
- Slice 6: [Preserve and act on human feedback during automation](https://github.com/hearthctl/kelos-orchestrator/issues/7) — blocked by #6.
- Slice 7: [Merge exactly the commit approved by a human](https://github.com/hearthctl/kelos-orchestrator/issues/8) — blocked by #2.
- Slice 8: [Assess unattended merge suitability through Kelos](https://github.com/hearthctl/kelos-orchestrator/issues/9) — blocked by #5, #8.
- Slice 9: [Integrate published components into Hearth](https://github.com/hearthctl/kelos-orchestrator/issues/10) — blocked by #3, #4.
- Slice 10: [Verify the complete workflow and operating procedure](https://github.com/hearthctl/kelos-orchestrator/issues/11) — blocked by #7, #9, #10.

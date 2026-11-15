# Human approval binding: issue #8 feasibility

Status: decision adopted after investigation, 2026-11-15. Humans approve with `lgtm <full SHA>` comments. Go performs a final gate recheck and SHA-conditional merge; the remaining hold/feedback race is accepted. The evidence below explains why label-only approval was rejected.

A human-applied `lgtm` label cannot currently be proven to identify the intended PR head using the documented polling APIs. Do not bind it to the first observed head. This is a finding, not a change to the agreed interaction or ADRs. Issue #8's remaining implementation and live merge acceptance criteria remain unmet.

## Scope and evidence

Read specification [issue #1](https://github.com/geoberle/kelos/issues/1), [issue #8](https://github.com/geoberle/kelos/issues/8), GLOSSARY.md, docs/spec.md, docs/ticket-plan.md, and ADRs 0001–0006. ADR 0004 supersedes 0001; outbound polling and direct commit-specific merging remain constraints. Read the requested TDD skill. No runtime, design decision, GitHub resource, deployment, or package visibility was changed.

The prepared `issue-8-feasibility` checkout includes `dffa74fc4260d102732ba8c434c44182949ef5ef`; `git merge-base --is-ancestor` succeeded for both HEAD and local `integration/issue-to-merge`. The integration branch initially pointed to that exact commit.

The GitHub connector returned 404 for both issues; authenticated read-only `gh api` requests succeeded. API observations below are limited samples, not a mutation experiment or proof of undocumented behavior.

## Label and head history

The REST [issue event contract](https://docs.github.com/en/rest/using-the-rest-api/issue-event-types#labeled) describes `commit_id` as the commit referencing the issue, not the head when a label was applied. A non-null value therefore cannot be treated as approval binding. GraphQL [LabeledEvent](https://docs.github.com/en/graphql/reference/issues#labeledevent) has actor, creation time, identity, label, labelable and intent fields, but no historical head. Reading its linked PR returns current state.

Live GraphQL introspection confirmed those fields. `HeadRefForcePushedEvent` exposed `beforeCommit` and `afterCommit` as nullable Commit objects, along with actor, createdAt, id, pullRequest and ref. That can explain some force pushes, but does not prove complete ordinary-push history or guarantee recovery when commit objects are unavailable. `PullRequestCommit` exposed commit, id, pullRequest, resourcePath and url: no push timestamp or before/after ref transition.

Read-only `GET /repos/geoberle/kelos/issues/12/timeline` returned four committed entries (2265dfa, ebf5baf, 8beb527, dffa74f), each with no event id or created_at. There were no labeled or force-push events in this sample, so their runtime values were not empirically demonstrated. Commit author/committer dates are not push times. Current commits alone cannot place an earlier label relative to an ordinary push.

Counterexample requiring proof: while polling is down, label A's head, then push B; compare pushing B first, then labeling it. A current head of B plus a label does not distinguish these approvals. Force pushes A→B→A also require invalidation under the existing new-commit policy even though the final SHA matches. No complete replay guarantee was established.

Identical timestamps must be treated as ambiguous. Event IDs identify records; neither numeric sorting across event kinds nor opaque GraphQL cursors establishes a documented transactional ordering of label application against ref updates. Pagination is not a snapshot transaction. Even a stable chronological presentation would not supply missing push transitions. Repeated reads, polling faster, or a quiet period cannot prove the intended SHA.

The separate [repository activity Events API](https://docs.github.com/en/rest/activity/events) is not a durable substitute: its documented window is at most 300 events and 30 days, with delayed availability. These limits apply to that activity feed, not to the issue timeline. Recovering selected PushEvents does not establish completeness during arbitrary downtime.

Reproduce the schema checks without mutations:

```sh
gh api graphql -f query='{ label: __type(name:"LabeledEvent") { fields { name } } force: __type(name:"HeadRefForcePushedEvent") { fields { name type { kind name ofType { name } } } } }'
gh api graphql -f query='{ __type(name:"PullRequestCommit") { fields { name } } }'
gh api --paginate repos/geoberle/kelos/issues/12/timeline
```

## Authorization

Keep the existing explicit human allowlist (`geoberle` initially); do not silently expand it to all maintainers. Label capability itself is not merge authorization. If admin/maintain eligibility is selected as an additional policy, use [repository permissions](https://docs.github.com/en/rest/collaborators/collaborators#get-repository-permissions-for-a-user): `permission` maps maintain to write, whereas `role_name` distinguishes maintain. Accepting every write actor would broaden that policy. Unknown custom roles and unavailable permission evidence require an explicit decision, not an inferred grant.

The read-only check for geoberle returned `permission: admin`, `role_name: admin`. This proves current permission only; the endpoint supplies no role-at-label-time history. Revocation and promotion during downtime need defined semantics before implementation. Actor identity, allowlist membership and current eligibility must not be replaced by comment author association or the identity of the polling credential.

## Direct merge and remaining races

The synchronous [merge endpoint](https://docs.github.com/en/rest/pulls/pulls#merge-a-pull-request) supports `sha` and `merge_method: merge`, and documents 409 on head mismatch. This protects the head, not a snapshot of labels or feedback. A successful response must actually report a merge; an ambiguous transport failure requires reading merge state before retrying. No merge request was sent here.

Code evidence in the pinned [go-github v80.0.0 pulls.go](https://github.com/google/go-github/blob/v80.0.0/github/pulls.go): `PullRequestOptions.SHA` and `MergeMethod` are serialized by `PullRequestsService.Merge` into the synchronous request. [issues_events.go](https://github.com/google/go-github/blob/v80.0.0/github/issues_events.go) exposes optional CommitID; it supplies no stronger label-head semantics. [repos_collaborators.go](https://github.com/google/go-github/blob/v80.0.0/github/repos_collaborators.go) exposes RoleName. Local admission code already uses the direct SDK. No wrapper or runtime extension is needed for this research.

Concrete race: poll sees approved SHA H, no hold and no pending feedback; a human adds hold or actionable feedback; merge with sha=H can still succeed. SHA equality cannot detect either change. Re-reading immediately before merging narrows but does not close this interval. Publishing a required status from polling has the same stale-green interval.

[Branch protection](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-protected-branches/about-protected-branches) provides server-side required checks and conversation resolution. Rules must apply to the actual merger without bypass, and allow merge commits. They do not by themselves implement arbitrary hold labels or conversation-comment invalidation. A green-check approximation must not be described as strict all-checks-passed enforcement without checking the configured rule semantics. This investigation did not verify deployed rules or experimentally validate draft/thread/CI races. Strict hold/feedback enforcement remains a separate blocker even after SHA binding is solved.

## Alternatives requiring an explicit decision

1. **Approve an explicit full SHA in a comment**, for example `lgtm <40-character SHA>`. Persist actor, comment identity/version and SHA as evidence; label becomes a display of validated approval. This works for a PR author and survives downtime without guessing the approved head. Editing/deleting approval, revocation, feedback ordering and A→B→A invalidation still need defined handling. Ordinary feedback remains command-free; only approval changes.
2. **Use an authorized approving GitHub review with its recorded commit_id**, then display `lgtm`. The [reviews API](https://docs.github.com/en/rest/pulls/reviews#list-reviews-for-a-pull-request) returns commit-bound review records. Require the exact current SHA and valid review state; do not infer it from submission time. Native self-approval restrictions make this unsuitable as the sole path when the authorized maintainer authors the PR. Dismissal and feedback races still require resolution.

The operator selected alternative 1 after this investigation. They preserve approval of a particular commit while changing the input interaction. Neither makes label/feedback reads atomic with merge. If strict publication cancellation cannot be represented by server-enforced state or agreed coordination, retain human direct merging rather than claim automated guarantees that the API does not provide.

## Validation and handoff

Verified ancestry, read-only issue/timeline/permission responses, live schema fields and pinned SDK serialization. The first introspection request exceeded GitHub's two-field introspection limit; splitting it succeeded. No test PR mutations were performed. No Go tests, Kubernetes tests or live positive/negative merge tests were run: this change is documentation only and does not implement behavior.

After an interaction and race contract are explicitly selected, implementation must use the approved GitHub HTTP-fixture/Kubernetes API boundary, table-driven Go cases and golden fixtures. Cover downtime, missing history, equal timestamps, ordinary and force pushes, A→B→A, unauthorized/revoked actors, edited/deleted approval, stale SHA/409, hold/feedback arrival and ambiguous merge responses. Fixtures can verify conservative controller behavior; they cannot prove stronger GitHub atomicity than the API provides. Continue using direct SDKs, logr, len emptiness checks and raw/validated/completed options.

Issue #8 may proceed with the selected full-SHA comment interaction and accepted final-recheck race. Label-only automated merging remains unsupported.

# GitHub request budget

The controller authenticates as a GitHub App installation, which may send 5000
requests per hour. Each poll reads the state of every managed pull request and
ready issue, so the cost grows with them.

## Conditional requests

The GitHub client sits on an in-memory HTTP cache and sends every repeated read
as a conditional request. GitHub answers an unchanged resource with 304, which
does not count against the rate limit. GitHub would permit reusing a response
for a minute; the controller revalidates on every use instead, because it reads
back what it just wrote and must not act on a stale answer.

The cache lives in the process and holds one response per URL, so it grows
with the pull requests, issues and workflow runs seen. It has no eviction; the
controller empties it once a day. The poll after that pays for every read once.

## Work done once

- Review evidence is evaluated once per poll for each pull request head and
  shared by review, editor and merge assessment. The rechecks made immediately
  before publishing `review-done` and before authorizing a merge are fresh.
- The artifacts of a review run attempt never change and are downloaded once.
- An editor Task whose result is recorded is not processed again.

## Verify on the current base

For each approved pull request that passed every other gate, a poll reads the
base branch head and the statuses of the pull request head. Both are answered
with 304 while nothing changed, so a poll without changes still sends no
counted request. A pull request whose verify run is unfinished costs one
counted read of that run per poll; only one run per base branch is under way.
Starting a run costs two writes, recording its result one.

## Measuring

Every poll logs `Poll finished` with `githubRequests` (requests sent),
`notModified` (304 answers among them) and `rateLimitRemaining` (as GitHub last
reported it). The requests that count against the limit are `githubRequests`
minus `notModified`.

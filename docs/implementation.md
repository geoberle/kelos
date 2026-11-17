# Implementation Tasks: in-progress label, log link, stop report and retry

Kelos sets a Task's phase from the worker's exit code. An agent that cannot
complete its issue still exits cleanly, so Kelos reports `Succeeded` and the
issue shows no problem. Optional `configuration.implementation` with `namespace`
(where the implementation TaskSpawners create Tasks) and the dedicated
`controllerLogin` makes the orchestrator report such Tasks and lets a comment
retry them.

## In-progress label

An open issue carries `kelos-in-progress` while its Task is running or has
finished with a pull request. The orchestrator sets the label once per Task and
removes it when the issue closes, which a merged pull request does through
`Closes #N`, when the Task stopped without a pull request, and when the Task is
gone. A pull request that is closed without merging leaves the label on its
open issue until the issue closes or a retry stops.

## Log link

With `consoleURL` set to the origin of the Kelos Console, the orchestrator
comments once on the issue of every Task that has not finished, with a link to
the Task's log in the Console. A retried Task gets its own comment. The Console
has an address for a Task's log but none for its page, so the link shows the raw
log: the latest 2,000 lines, and only to a browser that is logged in. A Task
that finishes within one poll gets no link.

## Final result

The implementation prompt must require the agent's final message to be exactly
one JSON object:

```json
{"state":"completed","pullRequest":"https://github.com/owner/repo/pull/9"}
```

```json
{"state":"stopped","reason":"why the issue is not complete","unmet":["each unmet criterion"],"next":"what a human must do before a retry can succeed"}
```

Kelos stores the final message in the Task's `status.results.response`.

## Stop report

Each poll lists the Tasks that carry the `kelos.dev/taskspawner` label in the
namespace. For a finished Task of a configured repository whose source is an
issue, the orchestrator comments once on that issue unless the result is
`completed` with a pull request. The comment states why the Task stopped, the
unmet acceptance criteria, how to proceed, the branch holding the work, and the
retry command. A Task that Kelos marks `Failed`, and a `Succeeded` Task whose
final message is not a valid result, are reported the same way with what is
known. No label changes.

A `Succeeded` Task is not judged during the first minute after its completion
time, because Kelos may still be capturing the final message from the Pod log.

Every issue-sourced TaskSpawner in the namespace is treated as an implementation
spawner. A Task that finished before its prompt demanded the final result has no
valid result and is reported as stopped on the first poll.

Agent and Kelos text is quoted line by line. Kelos reads a comment line equal to
the run command as a trigger, and the controller is an allowed user, so an
unquoted line could retry the Task forever.

The comment carries the Task UID, and the Task is annotated
`orchestrator.kelos.dev/stop-reported: "true"` afterwards. Annotated Tasks cost
no GitHub requests on later polls. The Role allows only listing and patching
Tasks.

## Retry

The implementation TaskSpawner sets `commentPolicy.triggerComment: /kelos run`
and `commentPolicy.allowedUsers` with the human authority and the controller
identity. Kelos then requires the command before it starts a Task, so admission
posts it once per issue. When an allowed user posts the command after a Task
finished, Kelos deletes that Task and starts a new one on the same branch. The
deleted Task's log and results are gone; the stop report on the issue remains.

Without `allowedUsers`, anyone who can comment on a public repository can start
agent runs.

An issue that was admitted before the trigger comment was configured carries
`agent-start` but no command. It starts when an allowed user posts the command.

# Retry implementation by run command

An implementation Task that ends without completing its issue is reported on
the issue and retried only when an authorized human comments `/kelos run`.
There are no automatic retries, and no label changes on a stop.

Kelos provides the mechanism: with a trigger comment configured, it replaces a
finished Task when an allowed user's command is newer than the Task's
completion. Kelos then requires the command for the first run too, so issue
admission posts it once per issue. This replaces the earlier rule that a
finished Task is kept forever as proof that its issue was handled.

Whether a Task completed its issue is taken from the agent's own final result,
not from Kelos's Task phase, which only reflects the worker's exit code.

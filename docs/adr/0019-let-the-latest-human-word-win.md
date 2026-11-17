# Let the latest human word win

A human can ask for a change on a pull request and can approve it with
`/lgtm`. Nothing decided between the two. A request stayed pending until an
editor fixed it, so a request the editor answered with `needs-human` could
never be cleared. When the human then approved, the merge loop set `lgtm` on
every poll because the approval was valid, and the editor loop removed it on
every poll because a request was pending. On hearthctl/pi5-bootlab#81 the label
changed about once a minute for an hour and a half, and each change started a
workflow run.

The latest word wins. A request written before the human's newest approval is
dismissed: "merge it as it is" overrules "please change something". A request
written or edited after the approval stays pending and withdraws the approval,
as before. The controller names the dismissed requests once per approval, so a
forgotten request does not vanish silently. A finished editor Task whose
requests are all gone no longer blocks the pull request.

Letting a pending request always win was rejected because the dead end would
remain and need a new command to clear a request by hand. Letting an approval
always win was rejected because a request posted after an approval would be
ignored until the head changes.

The request that started this was a `/kelos run` comment on a pull request,
where the command means nothing. A comment that carries any of the controller's
or Kelos' commands is never a repair request.

# First real Cowork task on the demo box, 2026-08-11

`cowork-task-done.png` is the agent workspace at
`https://chat-hive.scubed.co/agent-workspace`, signed in as the demo account
through the supported one-time-token helper (`docs/live-test-auth.md`), after
this branch was deployed to the box.

What it shows, and why each part could not appear on the previous build:

* Two coding-pack tasks in **Done** with a result the agent produced inside its
  own Apptainer sandbox, reached through the model gateway. Before this branch
  every task on this deployment ended **Blocked**, because control-plane could
  not exec Apptainer from its container at all.
* The **Blocked** task lower in the same list is one of those earlier attempts,
  left in place as the contrast.
* No "the agent runtime is not configured on this deployment" banner. That
  notice used to be derived from any task in the list, so a task blocked before
  the runtime existed kept it on screen forever, including above tasks that had
  just run. Its absence here is behaviour only the new agent-console build
  produces, which is what makes this screenshot evidence that the deploy
  actually replaced the running code rather than reporting that it had.

Observed timeline for the run in the screenshot, from the capture log:

```text
12:04:33  Queued    waiting for a sandbox
12:04:56  Running   sandbox up, conversation started
12:20:29  Done      result returned
```

Nothing in this directory contains a credential: the capture carries no URL
overlay, and the page shows no token, key or session value.

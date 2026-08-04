# PR #714 visual proof: Open WebUI default user avatar

Fix under proof: `deploy/docker/owui-static/user.png` (PR #714, defect 2 of #712).
`open_webui.routers.users.get_user_profile_image_by_id` falls back to
`FileResponse("/data/static/user.png")` for every OAuth-provisioned account, and that
file did not exist in the read-only bind mount, so every avatar request 500d.

Both screenshots are the first-attempt failure screenshot Playwright captured for
`01-chat-send-stream.spec.ts` in the owui-nightly job, on GitHub's hosted runner.
Same spec, same page state, same 1280x720 viewport, so the avatar slots are directly comparable.

| File | Source |
| --- | --- |
| `before-run-30942160601-01-chat-send-stream.png` | run 30942160601, sha 4275148 (before the asset was added) |
| `after-run-30944942989-01-chat-send-stream.png` | run 30944942989, sha 9765bad0 (after the asset was added) |
| `owui-default-avatar-before-after.png` | annotated side by side of the two above, with 5x insets of both avatar slots |

Server-side counterpart, from the `owui-compose-logs` artifact of the same two runs:

* before: `GET /api/v1/users/{id}/profile/image` returned 500 fifteen times
  (`RuntimeError: File at path /data/static/user.png does not exist.`)
* after: the same endpoint returned 200 eleven times, and the run's compose log contains no 5xx at all

The four failing chat specs in both runs are the pre-existing gateway 403 tracked in #717,
unrelated to this fix. `deploy/docker/owui-static/user.png` is byte identical at the proof sha
9765bad0 and at the PR head after its rebase onto #711 (blob 1e12e6e4, 1944 bytes), so the
rebase did not change what these screenshots show.

No credential is in frame. The only account identifier visible is the fixture address
`owui-e2e@hive-e2e.invalid`, a non-routable `.invalid` TLD already published throughout
the PR body and the workflow logs. Playwright captures the page only, so there is no URL bar.

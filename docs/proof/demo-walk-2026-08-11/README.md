# Demo readiness re-walk, 2026-08-11 afternoon

Captures for the re-walk of issue #858, run against the deployed demo box after
`c1ca04d5` (PR #870) shipped. The walk covers the owner's six demo capabilities:
chat, embeddings, voice to text, knowledge work, Cowork, and the coding agent.

Sessions came from `apps/web-console/tests/e2e/support/live-auth.mjs`, the audited
magic-link helper documented in `docs/live-test-auth.md`. No password was set,
reset or rotated. Nothing on the box was restarted, redeployed or reconfigured.
The one API key minted for the developer-API probe was revoked at the end of the
run, and its value never reached a log or a pixel: `17-console-api-key.png` was
taken after the secret node was overwritten with `hk_REDACTED_BY_CAPTURE`.

Everything the walk created was deleted afterwards: both knowledge collections,
every chat, and the API key.

| Capture | What it shows |
| --- | --- |
| `01-chat-signed-in.png` | Signed in through "Continue with Hive" on the deployed chat surface. |
| `02-chat-answer.png` | Chat: Enter sent the message and the model answered with the exact requested marker. |
| `07-voice-recording.png` | Voice: the composer recording a spoken sentence from the microphone. |
| `12-voice-transcript-in-composer.png` | Voice to text: the spoken sentence transcribed back into the composer. |
| `09-knowledge-picker.png` | Knowledge: the `#` picker offering the collection created during the walk. |
| `10-knowledge-attached.png` | Knowledge: the collection attached to the question. |
| `11-knowledge-answer.png` | Knowledge work: "Retrieved 1 source" and the codename that exists only in the uploaded document. |
| `06-knowledge-workspace.png` | Workspace > Knowledge listing the collection created through the API. |
| `13-cowork-*.png` | Cowork: a knowledge-work task composed, submitted, and picked up by the runtime. |
| `14-coding-*.png` | Coding agent: a coding task composed, submitted, and running. |
| `18-agent-workspace-status.png` | Both agent tasks finished: the coding task reports "The 10th Fibonacci number is: 55." |
| `15-console-overview.png`, `16-console-analytics.png`, `19-console-analytics-7d.png` | Console overview and analytics reading zero while the same workspace's account had settled traffic minutes earlier. |
| `20-console-model-catalog.png` | Model catalog as the presenter would show it. |
| `17-console-api-key.png`, `21-console-api-key-revoked.png` | An API key minted live for the developer-API probe, and revoked after. |
| `22-knowledge-after-cleanup.png` | Workspace > Knowledge after the walk's collections were deleted. |
| `walk.log` | Timestamped transcript of every step, redacted. |
| `results-part*.json` | Machine-readable outcome per phase. |

The `03-voice-ui.png` and `04`/`05` captures are from the first pass, where the
`#` attach was mis-driven by the automation rather than by the product; the
corrected pass is `09` through `11`.

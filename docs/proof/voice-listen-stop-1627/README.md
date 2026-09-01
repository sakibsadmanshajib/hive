# Voice mode stops listening, issue #1627

Captured on 2026-09-01 against the forked chat front end running in Docker,
twice: once from an image built at `origin/main`, once from an image built with
this pull request's head commit, rebuilt and recaptured after every review
finding landed, including the calibration one. Same harness, same synthetic room, same account
flow, two builds. The screenshots are attached to the pull request through
`scripts/post-pr-visual-proof.sh`; the logs are here because
`npm run lint:proof-tokens` scans this directory and nothing else.

## The two runs

| | Before, `origin/main` | After, this branch |
| --- | --- | --- |
| Recorder started | +0.29s, on room noise, before anyone spoke | +1.13s, when speech actually began |
| Recorder stopped | never, still running at +37.4s when the capture ended | +5.14s, two seconds after speech ended |
| Overlay state | `Listening...` for the whole 37.4s | `Listening...` then `Thinking...` then `Tap to interrupt` then back to `Listening...` |
| Transcription requests | none, the recorder never released any audio | exactly one |
| Chat completions | none | exactly one, and the transcript reached the model |
| Further triggers over the next 33s of room noise | n/a, still holding the first one | none |

`capture-before.log` and `capture-after.log` are the runs. `stub-requests-before.log`
and `stub-requests-after.log` are what the upstream stub received, which is the
half of the issue about a flooded transcription endpoint: before, the endpoint
is never called at all because the recorder never stops; after, it is called
once per utterance and not once for room noise.

## What the microphone was

Chromium's own fake capture device (`--use-fake-device-for-media-capture`)
enumerates zero audio devices inside the Playwright container, on both the
headless shell and the full chromium channel, so `getUserMedia` is shimmed in
the page with a Web Audio stream. Everything downstream of that call is the
real thing: a real `MediaStream`, the real analyser, the real `MediaRecorder`,
the real component, the real backend. What is synthetic is the room, and its
shape is the whole point:

* a low frequency floor, two tones at 90Hz and 123Hz plus a hiss, about -40 dBFS
  combined. This is what an ordinary room sounds like and it is what the old
  trigger heard as speech. Flat white noise at the same total energy would
  spread across a thousand analyser bins, leave every bin reading zero, and let
  the pre-fix build look like it worked. That was tried first, and it did.
* one two second burst of voiced signal, a harmonic stack with a four hertz
  syllabic envelope, starting one second after the overlay opens.

The instrumentation only observes: it timestamps `MediaRecorder.start` and
`stop` and counts live microphone tracks. The same script runs against both
builds.

## Reproducing

`capture.mjs` and `stub.py` are committed here. The rest:

```bash
docker build -f deploy/docker/Dockerfile.open-webui -t hive-owui:proof-1627 .
docker network create proof1627
docker run -d --name proof1627-stub --network proof1627 \
  -v "$PWD/docs/proof/voice-listen-stop-1627:/work" -v "$PWD/out:/out" \
  -w /work python:3.12-alpine python stub.py 8000
docker run -d --name proof1627-owui --network proof1627 -p 127.0.0.1:3402:8080 \
  -e WEBUI_SECRET_KEY=proof1627-local-secret -e ENABLE_SIGNUP=true \
  -e OPENAI_API_BASE_URL=http://proof1627-stub:8000/v1 -e OPENAI_API_KEY=proof1627-stub-key \
  -e DEFAULT_MODELS=hive-default \
  -e AUDIO_STT_ENGINE=openai -e AUDIO_STT_OPENAI_API_BASE_URL=http://proof1627-stub:8000/v1 \
  -e AUDIO_TTS_ENGINE=openai -e AUDIO_TTS_OPENAI_API_BASE_URL=http://proof1627-stub:8000/v1 \
  hive-owui:proof-1627
docker run --rm --network container:proof1627-owui \
  -v "$PWD/docs/proof/voice-listen-stop-1627:/work" -v "$PWD/out:/out" -w /work -u root \
  -e LABEL=after -e POLLS=140 -e OWUI_URL=http://localhost:8080 \
  -e PROOF_PASSWORD="$(openssl rand -hex 12)" \
  mcr.microsoft.com/playwright:v1.55.0-noble sh -c 'npm i playwright@1.55.0 && node capture.mjs'
```

The browser reaches the front end over `http://localhost:8080` by sharing the
chat container's network namespace, which is not a detail to drop: `getUserMedia`
is unavailable on an insecure origin, and a container hostname is one.

Nothing in this capture is wired into a workflow, for the same reason the
`voice-response-format-1562` capture is not: no CI lane boots this front end.
What does run pre-merge is `vendor/open-webui/src/lib/hive/voiceActivity.test.ts`,
in the `make test-owui-frontend` job and again inside the image build, and it
covers the decision this capture exercises, twenty cases including the hard cap,
the room-noise case and a room that was already loud when the call opened, at a
hundred percent of the module.

## The other half of the issue

The issue also reports rate limiting, and that half is not a voice defect. See
the pull request body for the evidence: none of the 429s measured in the LiteLLM
logs came from the transcription route, and the transcription path already
carries a bounded retry ladder.

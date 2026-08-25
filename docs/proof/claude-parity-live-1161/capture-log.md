# Visual proof capture log, PR #1161 post-deploy live check

- Date: 2026-08-25
- Target: https://chat-hive.scubed.co running the deployed build at sha fe7eccd7 (PR #1161 merged to main and landed on the demo box by deploy-demo-box.yml).
- Method: headless chromium driven by Playwright against the live deployed surface. Sign-in used the admin one-time-token magiclink mint documented in docs/live-test-auth.md. No password was set, reset or rotated anywhere in the flow. An ssh tunnel to the box's GoTrue served the admin mint step only; all page traffic went to the public chat hostname over HTTPS.

## What the capture shows

The full-window screenshot of a live signed-in chat session after one assistant reply demonstrates the parity behaviors from #1161 on the deployed build:

1. Assistant prose renders in the serif register (Source Serif 4) at the shared reading measure while sidebar and composer chrome stay in Hanken Grotesk.
2. The reasoning disclosure control is present above the assistant reply and expands its reasoning text.
3. The composer model chip identifies the active model as "Hive Auto".
4. The Hive navigation sidebar is present with its Hive-branded entries.
5. The credits line renders for the signed-in user.

## Artifact pointers

- Permanent uploaded image (visual-proof-assets GitHub Release, branch-delete safe): https://github.com/sakibsadmanshajib/hive/releases/download/visual-proof-assets/pr1161-20260825185531-8440-hive-live-1161.png
- Inline PR comment carrying the same image: https://github.com/sakibsadmanshajib/hive/pull/1161#issuecomment-5415211291

## Credential hygiene

No token-bearing URL appears in the screenshot pixels or anywhere in this log. The minted one-time token existed only inside the mint request and the browser session it created, never in a page URL that reached a captured frame. The Playwright session storage state file was deleted immediately after capture. Verified against the lint:proof-tokens pattern set before upload.

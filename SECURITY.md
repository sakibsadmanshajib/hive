# Security Policy

## Project Overview

Hive is an OpenAI-compatible API gateway. The Go control-plane and edge-api handle routing, metering, and billing; the Next.js web-console is the developer console; the chat frontend is a heavily modified fork of Open WebUI; agent workloads run inside Apptainer sandboxes.

## Supported Versions

Hive is a pre-1.0 product with continuous deployment to the demo environment. Only the latest `main` branch of this repository is supported. We do not maintain security branches for older releases or tags; if you are running anything other than a recent build of `main`, upgrade before reporting or expecting a fix.

## Reporting a Vulnerability

Please do **not** open a public GitHub issue for a vulnerability.

This repository has GitHub private vulnerability reporting enabled. Use it: go to the repository's **Security** tab and click **Report a vulnerability**. This keeps the details private while we triage and gives you a direct channel to the maintainers.

**Expected response times:** every report is triaged within 5 business days. After triage, we will communicate an assessment and an expected fix timeline in the private thread.

## Scope

The following areas are in scope:

- Authentication and session handling
- Tenant isolation, including row level security boundaries between tenants
- Money and billing paths (credits, reservations, settlement, payments)
- Provider API key handling and any path where provider credentials could leak
- Sandbox escape from the Apptainer-based agent execution environment
- Injection flaws (SQL, command, template, prompt-adjacent injection with real impact)
- Exposure of secrets, tokens, or internal configuration

The following are out of scope:

- Social engineering of staff, users, or support channels
- Denial of wallet through paid load testing without prior coordination with us
- Findings against `demo.scubed.co` endpoints that only demonstrate missing rate limits already tracked as known work

## Safe Harbor

We will not pursue action against good-faith security research that follows these rules:

- Research only against your own accounts and your own data.
- Never access, modify, or exfiltrate another tenant's data. Ever.
- No destructive testing: no service degradation, no data deletion or corruption.
- Report findings to us privately before any public disclosure.
- We follow a 90 day coordinated disclosure window from first report.

If you act in good faith within these bounds, we consider your activity authorized and will work with you on resolution and credit.

## Priority Note

Bugs in money-path handling (billing, credits, reservations, settlement) have standing priority per project decision D-034, which requires those paths to fail closed. Reports in this area will be treated as high severity.

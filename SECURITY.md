# Security Policy

## Reporting a Vulnerability

Do not open a public GitHub issue for a suspected security vulnerability.

Instead:

1. Use GitHub private vulnerability reporting if it is enabled for this repository.
2. If private reporting is not available, contact the repository owner privately through GitHub at `https://github.com/sakibsadmanshajib` before any public disclosure.

Include the following in your report:

- affected component or file path
- reproduction steps or proof of concept
- impact assessment
- any mitigation you already identified

## What Not to Share Publicly

Never post the following in public issues, pull requests, or comments:

- API keys, tokens, or credentials
- private Supabase keys or admin tokens
- raw production-like billing or user data
- internal provider diagnostics that should remain protected

If you accidentally expose a secret, rotate it immediately and notify maintainers privately.

## Scope

Security-sensitive areas in this repository include:

- authentication and session handling
- billing, credits, refunds, and ledger behavior
- API key creation, storage, and validation
- provider status and metrics boundaries
- infrastructure or deployment configuration

## Maintainer Handling Expectations

Maintainers should:

- acknowledge credible reports promptly
- avoid public disclosure until a fix or mitigation is ready
- preserve evidence needed to reproduce and fix the issue
- coordinate doc and changelog updates when user- or operator-facing behavior changes

## Safe Contribution Rules

- keep public endpoints sanitized when they are meant to be public
- keep internal diagnostics behind the intended admin-token protection
- avoid adding examples that encourage committing secrets or disabling protections

## Disclosure

Please allow maintainers reasonable time to investigate, fix, and communicate a vulnerability before public disclosure.

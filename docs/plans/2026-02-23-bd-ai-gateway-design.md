# BD AI Gateway Design

## Product Direction
- API-first aggregator with OpenAI-compatible endpoints.
- Bangladesh-first go-to-market with local payment providers.
- Closed-loop AI credits system instead of BDT wallet semantics.

## Billing Rules
- Base conversion: `1 BDT = 100 AI Credits`.
- Campaign-based bonus conversion allowed.
- Refund: `100 AI Credits = 0.9 BDT` for unused purchased credits only.
- Default refund window: 30 days.

## Key Constraints
- No shared-account plans.
- Verified payment event required before credit minting.
- Request-level usage and credit tracking for auditability.

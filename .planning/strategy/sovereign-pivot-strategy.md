# Sovereign Pivot Strategy

Decision document. Status: owner-decided pivot, recorded for execution.

This document is general information for strategy and product planning only. It is not legal, tax, or compliance advice. Several regulatory citations below are flagged as live-unverified and must be confirmed with qualified counsel before any customer-facing claim is made.

## Thesis

Hive sells data sovereignty and private inference to regulated and data-sensitive organisations that cannot or will not send their data to public AI services. The product is an on-premises Hive box, EnterpriseEdge, that runs the full OpenAI-compatible gateway stack on the customer's own hardware so sensitive data never leaves the customer's control. The wedge is structural, not feature based: even a vendor's strongest privacy promise cannot defeat the US CLOUD Act reach over US-controlled cloud providers, and prompts sent to public AI services can be retained and become discoverable in litigation. On-premises inference removes the third party entirely, which is the only posture that closes both exposures. Geography focus is Canada, ideally Ontario, where funding and credits can run through the WEtech Alliance in Windsor-Essex.

## Target market and verticals

Primary buyers are organisations that hold data they are legally or contractually barred from exposing to a third party, or that face heightened discovery and recordkeeping risk:

- Banks, credit unions, and other federally or provincially regulated financial institutions
- Insurers and InsurTech
- FinTech handling customer financial data or material non-public information
- Healthcare providers and custodians of personal health information
- Law firms and in-house legal teams bound by privilege and confidentiality duties
- Any company with a hard policy against placing its data in someone else's cloud

Buyer profile: small firms buy a single box and grow into multi-GPU then multi-device as load increases. The same product line scales from one regulated practice to an enterprise deployment.

## The two wedges

### Wedge 1: Sovereignty versus the US CLOUD Act

- The US CLOUD Act (2018, codified at 18 USC 2713) lets US-jurisdiction providers be compelled to disclose customer data regardless of where that data is physically stored.
- Data residency (bytes located in Canada) is not data sovereignty (control of the jurisdiction that governs the provider). A Canadian region of AWS, Azure, Google, Oracle, or IBM is still CLOUD-Act-reachable because the operating provider is US-controlled.
- CLOUD-Act-free options are providers with no US presence. OVHcloud (French) and DAIR or CANARIE (Canadian non-profit) are not exposed in the same way. On-premises hardware, owned by the customer, involves no third party at all and is the strongest protection.
- Honest caveat: a managed Hive Cloud hosted on a US provider does not deliver sovereignty. The Canada-US Mutual Legal Assistance Treaty (MLAT) still permits a slower, court-supervised disclosure path. The full sovereignty story therefore lives in the on-premises box, not in any hosted tier on US infrastructure.

### Wedge 2: AI-prompt legal-discovery risk

- A US court (NYT v OpenAI, Southern District of New York) ordered OpenAI to preserve ChatGPT logs, including deleted and temporary or incognito chats. Exact date and scope to be verified; the primary court document was not independently verifiable, so the order is stated generically here.
- Enterprise and zero-retention API contracts explicitly carve out retention "unless legally required to retain" (OpenAI enterprise privacy policy), which means a preservation order overrides the no-retention promise.
- Prompts to public LLMs are electronically stored information (ESI). They are discoverable, held by a third party, and subject to subpoena, government request, and preservation orders that the customer does not control.
- Vertical-specific risk:
  - Law: privilege waiver exposure (NYC Bar Formal Opinion 2023-5; Law Society of Ontario advisory).
  - Finance: off-channel recordkeeping enforcement (the roughly 1.8 billion USD WhatsApp-style fines) and material non-public information handling.
  - Healthcare: breach of personal health information if sent to an endpoint without a business associate or equivalent agreement.
- On-premises inference removes the third party entirely. Data that is never sent out cannot be compelled from an external party by any external order.

## Regional regulatory map

General information only. Each row is tagged general-info-verify-with-counsel. Several citations are live-unverified and customers must confirm scope and current status with their own counsel.

| Region | Domain | Key obligation (general information, verify with counsel) | On-premises implication |
|--------|--------|-----------------------------------------------------------|--------------------------|
| Canada | Finance | OSFI Guideline B-10: client-data isolation; FRFI and OSFI hold audit rights over third parties. OSFI B-13: technology and cyber risk management. Quebec Law 25: privacy impact assessment required before transferring personal information outside Quebec. | On-premises keeps data inside the institution's own boundary, simplifying isolation and audit and removing the cross-border transfer trigger. |
| Canada | Health | Ontario PHIPA s.17: the custodian stays accountable for how a service provider handles personal health information. PIPEDA Principle 4.1.3: accountability for transfers, including foreign-law and CLOUD-Act access risk. | On-premises keeps personal health information under the custodian's direct control with no external service provider in the data path. |
| Canada | Legal | Law Society of Ontario Rules of Professional Conduct r.3.3: duty of confidentiality. | On-premises avoids disclosing client confidences to any third-party processor. |
| UK | Finance | FCA and PRA SS2/21: material-outsourcing audit rights, documented exit plan, concentration-risk management. UK GDPR: international-transfer controls. | On-premises removes the material-outsourcing relationship and the international-transfer step. |
| UK | Legal | SRA confidentiality duties. | On-premises keeps client matter data inside the firm. |
| UK | Health | NHS Data Security and Protection Toolkit (DSPT). | On-premises supports DSPT data-control expectations by keeping processing in-house. |
| Bangladesh | Finance | Bangladesh Bank ICT Security Guideline. | On-premises aligns with in-country control expectations. |
| Bangladesh | Data | Draft Personal Data Protection Act 2023 localization (status pending). | On-premises satisfies localization by keeping data in-country; confirm current draft status with local counsel. |

Compliance framing rule (hard, applies to every customer-facing surface): never state that Hive is PIPEDA, HIPAA, OSFI, or GDPR compliant. Say that Hive is designed to support data residency and on-premises control, and that customers should validate with their own counsel.

## Product and hosting decision

- The on-premises box, EnterpriseEdge, is the sovereign product. It runs the full stack on the customer's own hardware: single box is excellent for a small regulated firm, scaling to multi-GPU and then multi-device as load grows.
- A sovereign managed tier, when offered, is hosted only on OVHcloud or DAIR. It is never hosted on US-owned cloud, because US ownership reintroduces CLOUD Act exposure and breaks the sovereignty claim.
- US-owned cloud credits (Oracle, GCP, IBM, including WEtech-provided credits) are acceptable only for the non-sovereign demo and development environment. They must never carry real customer data and must never back a tier marketed as sovereign.

## Pricing model

- Per-box pricing, with each box tiered by capacity and capability.
- Compute add-on for additional GPU or throughput.
- Per-node pricing as the deployment scales to multiple devices.

This mirrors how the hardware itself scales: a customer starts on one tiered box and pays incrementally for compute and nodes as they grow.

## Go-to-market via WEtech and Ontario

- Anchor in Windsor-Essex through the WEtech Alliance: use the advisor relationship for introductions, structuring, and credits.
- Use US-owned WEtech credits (Oracle, GCP, IBM) for the dev and demo environment only, never for a sovereign tier.
- Use OVH or DAIR for any hosted demo that needs to stand in for the sovereign posture.
- Structure or register the business in Ontario to align with the regional funding and credit programs.
- Lead with the two wedges in plain language to regulated buyers; let the structural argument, not feature lists, carry the pitch.

## Honest caveats and risks

- MLAT: a hosted tier on US infrastructure remains reachable through a slower, court-supervised MLAT path. Sovereignty claims are valid only for the on-premises box.
- Customer outbound API calls: if the customer configures Hive to route to an external public provider, the sovereignty guarantee no longer holds for that traffic. The guarantee covers data that stays on the box.
- Unverified citations: several regulatory references in this document are live-unverified. Confirm current text, scope, and status before relying on them in any customer-facing material.
- Date-uncertain court order: the OpenAI log-preservation order is stated generically because the exact date and scope were not independently verifiable.
- Compliance overclaim risk: marketing must follow the hard framing rule above and never assert framework compliance.

## Open questions

- Which single vertical do we lead with first in Ontario: finance, healthcare, or legal?
- Reference hardware spec and bill of materials for the entry-tier single box.
- Do we offer the sovereign managed tier at launch, or on-premises only first?
- OVH versus DAIR for the hosted sovereign-grade option: which fits the first customers and the funding constraints?
- Partner or counsel to validate the regulatory map per vertical before any compliance-adjacent claim.
- Pricing anchors: entry-tier box price, compute add-on unit, and per-node increment.

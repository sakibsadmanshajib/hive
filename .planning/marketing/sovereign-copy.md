# Hive Sovereign Site: Final Website Copy

Source of truth: `website/sovereign/SPEC.md`, the sovereign-pivot strategy doc, and the LOCKED FACTS supplied by the owner. Company: S Cubed Technology Ltd. Canadian English throughout. No dash punctuation between clauses. Locked H1 preserved verbatim.

Builder note: each block below is labelled `[PAGE] / [SECTION]` so it can be dropped into the matching `.astro` route. Eyebrows, headings, body, CTAs, microcopy, footnotes, and disclaimers are all provided. Lines that need legal sign-off carry `[LEGAL-REVIEW]`. Mandatory verbatim disclaimers and footnotes are quoted exactly from SPEC and must not be reworded.

Tone discipline (the actual fix): regulated buyers distrust hype. Every claim either states a mechanism or cites a fact. No superlatives, no breathless promises, no empty adjectives. Confident and quiet. A general counsel should read a line and think "that is precise", not "that is a pitch".

Jurisdiction neutrality: no country is the villain. The problem is loss of control, not any one nation. The US CLOUD Act is used only as a neutral, factual illustration of a general principle: any cloud provider is subject to the laws of the jurisdiction it answers to, so sending data to any third-party cloud means accepting that jurisdiction's reach. Hosted infrastructure is framed as "in-region, under your own jurisdiction", never as anti-US. S Cubed intends to serve customers in the US, UK, Canada, and elsewhere, including US finance, healthcare, and defence.

Compliance placement reminder (structural, do not break): cost-proof components (Pinterest callout, cost-wedge) and residency-risk components (sovereignty two-series) never share a section band. Pinterest never appears on the Security page and never adjacent to a sovereignty claim. The court sentence appears only in its permitted verbatim form. "sovereign" is always scoped to the on-prem box in the same sentence. Never "compliant", "certified", or "ready" for any framework. Compliance language is designed-to-support only, mapped to the right vertical, never a certification claim and never claimed as in progress or planned.

Nationality slot: S Cubed is not yet Canadian-registered, so no "Canadian company" claim appears anywhere. Where a future nationality line can be slotted in once registration completes, it is marked `[NATIONALITY-SLOT]` and left unpublished.

---

# PAGE 1: HOME

Route: `src/pages/index.astro`. Scope: both. Narrative spine: the control problem stated as mechanism, the three exposures as fact, the three-tier spectrum teaser, the ownership cost story, who it is for, proof, clear CTAs.

## HOME / Hero (LOCKED: do not edit H1)

**Eyebrow:** Sovereign AI

**H1 (locked, verbatim):** Your AI runs on your hardware. Not ours. Not anyone's.

**Subhead (three teal-tick fragments):**
- Open-weight models on your own servers.
- No data leaves your building.
- No usage-based vendor bill.

**Scope clarifier (faint, no tick):** Self-hosted deployment. Your hardware, your network, your data.

**Primary CTA:** Book a Demo  →  `/book-a-demo`
**Secondary CTA:** See Pricing  →  `/pricing`

**Hero chart caption (sovereignty two-series, teal vs red):** With Hive on-prem, your data stays inside your network boundary. Data sent to a third-party cloud crosses that boundary, and once it has crossed, the jurisdiction that governs it is no longer only yours.

## HOME / The control problem (mechanism first)

**Eyebrow:** Control, not trust

**H2:** A data-protection promise is only as strong as the jurisdiction that can override it.

**Body:**
Every cloud AI provider will tell you your data is protected, and most of them mean it. The limit is structural, not a question of intent. Any provider is subject to the laws of the jurisdiction it answers to, so a contractual promise can be overridden by a lawful order from that jurisdiction. This is true of every cloud, in every country. It is the nature of handing data to a third party.

Hive changes the mechanism rather than the promise. The open-weight model runs on hardware you control. Your prompts, your documents, and the model's responses stay inside your own network boundary. On the on-prem deployment, inference runs locally and the request never calls an external API, so there is no third-party endpoint to subpoena and no external data path to audit. The data does not move, so there is nothing to compel.

That is what we mean by sovereignty, scoped precisely: the on-prem Hive deployment keeps every inference inside your own infrastructure, under your physical and legal control. [LEGAL-REVIEW]

**Control points (three short cards):**
- **You hold the hardware.** The box sits on your premises, or in an in-region datacentre you select, under your own jurisdiction. You hold the keys, the logs, and the machine.
- **No request leaves the boundary.** On the on-prem deployment, inference runs locally and never calls an external API. There is no provider in the loop, so there is no provider to be ordered to produce anything. [LEGAL-REVIEW]
- **One vendor relationship, no metered data path.** You license software and support from us. Your data never reaches us, because there is no place in the architecture for it to go. [LEGAL-REVIEW]

`[NATIONALITY-SLOT]` Once S Cubed completes Canadian registration, add a fourth card here: "**A Canadian company.** S Cubed is registered and operated in Canada." Do not publish this line until registration is confirmed.

## HOME / What you are exposed to today (CLOUD Act + discovery + metered cost)

**Eyebrow:** What a third-party cloud carries with it

**H2:** Three exposures come bundled with every cloud AI subscription.

**Body intro:**
When your team puts a contract, a patient record, or a quarter's unpublished numbers into a third-party AI tool, three things follow that most buyers never priced in. None of them is about a bad provider. All three are properties of sending data outside your own control.

**Exposure 1 : Reach that follows the provider, not the data.**
Any cloud provider can be compelled, under the law of the jurisdiction it answers to, to produce customer data regardless of where that data physically sits. The US CLOUD Act is one well-known example of this general principle: it lets a US-controlled provider be ordered to hand over data even when that data lives in another country. The point is not specific to one law or one country. Residency is where the bytes live. Sovereignty is who can be ordered to produce them. They are not the same thing, in any jurisdiction. [LEGAL-REVIEW]

**Exposure 2 : Your prompts become records someone else holds.**
A US court has already ordered a major AI provider to preserve chat logs. Prompts and responses sent to a third-party AI service are electronically stored information held by another party. They can be subpoenaed, preserved by order, and pulled into discovery, and a "we do not retain" promise gives way the moment a preservation order lands. The data you cannot see is the data you cannot govern. [LEGAL-REVIEW]

**Exposure 3 : A bill that grows with use.**
A third-party AI API meters you per token. Light, occasional usage stays cheap. But once AI runs inside long-running agents, automations, and sustained daily work, the meter never stops and the bill grows with every win. The more value you get, the more it costs to keep getting it.

**Transition line:** The first two exposures are about who can reach your data. The third is about who controls your cost. Running the model yourself addresses all three.

## HOME / The three-tier spectrum (teaser)

**Eyebrow:** A spectrum of control

**H2:** Run Hive at the level of control your data demands.

**Body intro:**
The same Hive software runs three ways, from maximum control to lowest cost. You choose the point that fits your data, your team, and your budget. Every hosted option runs only on infrastructure that sits in-region and under your own jurisdiction, because hosting you under a jurisdiction other than your own would reintroduce the exact exposure you came here to remove. [LEGAL-REVIEW]

**Tier teaser 1 : Own it (on-prem).**
A Hive deployment on your premises, run by your team. Data never leaves the building. The strongest position on the spectrum. [LEGAL-REVIEW]

**Tier teaser 2 : Colocate it.**
A dedicated Hive box, hosted by us in-region, on infrastructure under your own jurisdiction. We manage the hardware. Residency stays in-region.

**Tier teaser 3 : Shared cloud.**
A region-locked shared instance with a full audit trail. Lowest cost, fastest start. Honestly the weakest tier on the spectrum, and we grade it that way.

**Link CTA:** Compare the three tiers  →  `/how-you-run-hive`

## HOME / The ownership cost story (cost band: its own section, never beside a sovereignty claim)

**Eyebrow:** Own the model, stop renting the meter

**H2:** An open-weight model you own costs a fraction of the API you rent.

**Body:**
Open-weight models have closed much of the quality gap with the large proprietary APIs, and they run on hardware you can buy. Run the same workload on a model you own and the cost changes shape. There is a mostly fixed cost up front for the box, and after that inference is roughly the cost of the electricity it draws.

Measured on output, open-weight inference runs roughly 10x to 30x cheaper than the proprietary APIs. For an entry-tier box handling sustained daily work, the box pays for itself in about seven months, then keeps saving every month after. For light, occasional use, a public API is a reasonable choice. For long-running agents, automations, and heavy daily usage, ownership is the cheaper position over time.

**Cost-wedge chart caption:** Open-weight output runs roughly 10x to 30x cheaper than proprietary APIs.

**Cost-wedge mandatory on-face footnote (verbatim):** ~10x to ~30x cheaper

**Cost-wedge citation footer:** Rates as of June 2026. Hosted-API pricing, self-hosting economics differ.

**Pinterest proof card (cost context only, never beside a sovereignty claim):**
"Pinterest cut AI costs roughly 90 percent and raised accuracy roughly 30 percent by running and customising the open-weight Qwen3-VL model on its own infrastructure."

**Pinterest card mandatory disclaimer (same section, verbatim):** Results reflect Pinterest's specific implementation. Your outcomes will depend on your hardware, model choice, and workload.

**Pinterest card date stamp (mandatory):** as of June 2026

**Link CTA:** See the full cost model  →  `/pricing`

## HOME / Who it is for

**Eyebrow:** Built for confidential work

**H2:** If your data cannot go to a third-party AI service, Hive runs in your own.

**Body intro:**
Hive is built for organisations whose data is the one thing they cannot put at risk. That covers a wide range of work, in many jurisdictions.

**Audience cards (five):**
- **Financial institutions.** Banks, credit unions, insurers, and FinTech firms handling material non-public information, internal agents, and recordkeeping obligations that an external endpoint cannot satisfy.
- **Healthcare and health-data custodians.** Personal health information stays under the custodian's direct control, with no external service provider in the data path.
- **Government and defence.** Public-sector bodies and military programmes, including air-gapped and offline deployments where no outbound network path is permitted. [LEGAL-REVIEW]
- **Legal.** Privilege does not survive a third-party processor. Client matters and document review stay inside the firm.
- **Any sensitive-data organisation.** If "do not send our data to a third party" is a hard rule, on-prem inference is the posture that keeps it. Trade secrets, source code, R&D, and competitive plans included. [LEGAL-REVIEW]

**Link CTA:** See use cases by industry  →  `/use-cases`

## HOME / Continuous improvement (ownership without obsolescence)

**Eyebrow:** A platform, not a frozen box

**H2:** The box you buy keeps getting better.

**Body:**
Owning your hardware does not mean owning a snapshot. Under the annual licence, the Hive software and the supported open-weight models keep improving. New and stronger open models are validated and made available as they ship, so your deployment tracks the state of the art instead of falling behind it. When a workload outgrows a single box, capacity is added: one box, then multiple GPUs, then multiple nodes. You are buying a platform that keeps improving, on hardware you can extend.

**Link CTA:** See how you run Hive  →  `/how-you-run-hive`

## HOME / Competitive one-liner

**Eyebrow:** The difference in one line

**H2 / pull quote:** Glean, Writer, Cohere North, and Copilot route every query through their cloud. Hive runs in yours.

## HOME / Proof spine strip

**Eyebrow:** What is actually true here

**Proof point 1 : Architecture:**
Open-weight model weights run on your hardware. Every inference executes locally. No prompt, no response, no document calls an external API or leaves your network boundary. [LEGAL-REVIEW]

**Proof point 2 : Perplexity (with date stamp):**
Perplexity runs DeepSeek R1 on its own servers in US and EU data centres. User data never leaves Western servers. *(as of January 2025)*

**Proof point 3 : Regulatory category (general information, not advice):**
Finance, healthcare, and legal teams often cannot route sensitive data through an external AI endpoint. Self-hosted open weights keep every inference inside the network boundary. This is general information, not legal advice. [LEGAL-REVIEW]

## HOME / Closing CTA

**H2:** See it run inside your own network.

**Body line:** Book a short demo and we will show Hive running locally, with no data leaving the box.

**Primary CTA:** Book a Demo  →  `/book-a-demo`
**Secondary CTA:** See Pricing  →  `/pricing`

**Footer microcopy (entity):** S Cubed Technology Ltd.

---

# PAGE 2: HOW YOU RUN HIVE (NEW PAGE)

Route: suggested `src/pages/how-you-run-hive.astro`. Scope: both. Purpose: the three delivery tiers in depth, honestly graded, scannable, plus the extendable-hardware and continuous-upgrade mechanisms. This page discusses the on-prem tier with scoped sovereignty language and the hosted tiers with honest, non-sovereign framing. Pinterest and cost-proof components do not belong here.

## HOW YOU RUN HIVE / Hero

**Eyebrow:** How you run Hive

**H1:** One Hive. Three ways to run it. You choose how much control you keep.

**Subhead:** From a box you own and never let out of the building, to a region-locked shared instance you can start in a day, the same Hive software runs across the whole spectrum. You pick the point that matches your data and your budget.

**Primary CTA:** Book a Demo  →  `/book-a-demo`
**Secondary CTA:** See Pricing  →  `/pricing`

## HOW YOU RUN HIVE / The same software, three deployments

**Eyebrow:** One stack, no rewrites

**H2:** The software does not change. Only where it runs does.

**Body:**
Whichever way you run Hive, the underlying system is the same OpenAI-compatible gateway, with the same models, the same API surface, and the same admin console. Your applications do not change as you move along the spectrum. You can start on a shared instance to prove value, move to a dedicated colocated box as your data sensitivity grows, and bring the box fully on-prem when control becomes non-negotiable, without rebuilding your integration.

## HOW YOU RUN HIVE / The in-region infrastructure rule

**Eyebrow:** The one rule across every hosted tier

**H2:** No hosted tier runs outside your own jurisdiction.

**Body:**
This is the rule that makes the hosted tiers worth offering. The reason on-prem control matters is that any cloud provider is reachable under the law of the jurisdiction it answers to, wherever its datacentres sit. If we then hosted you under a jurisdiction other than your own, we would hand back the exact exposure you came to us to remove. So every hosted Hive tier runs only on infrastructure that is in-region and under your own jurisdiction. [LEGAL-REVIEW]

**Honest boundary callout:** A managed tier on any third-party infrastructure does not carry the same data-residency properties as the on-prem edition. If your hard requirement is that data stays on hardware you physically control, the on-prem tier is the one that meets it. [LEGAL-REVIEW]

## HOW YOU RUN HIVE / Tier 1: Own it (on-prem EnterpriseEdge)

**Eyebrow:** Maximum control

**H2:** Own it. A Hive deployment on your premises, run by your team.

**What it is:**
EnterpriseEdge is the full Hive stack running on a box that lives in your building, owned and operated by you. It starts at a single workstation-class box for a small regulated firm and scales to multi-GPU and multi-device as load grows. It is the same software the hosted tiers run, entirely inside your walls. Air-gapped and offline deployments are supported for environments that permit no outbound network path. [LEGAL-REVIEW]

**Who it suits:**
Teams whose data cannot leave the premises under any circumstances. Financial institutions handling material non-public information, healthcare custodians holding personal health information, government and defence programmes, and law firms protecting privilege.

**Sovereignty level (honest):** Highest. On the on-prem deployment, data never leaves your network boundary and no third party is in the data path. This is the only tier where the full sovereignty claim holds. [LEGAL-REVIEW]

**Ops burden:** You run it. The box, the updates, and the hardware sit with your team, backed by our support and your annual licence. Highest control, most operational ownership.

**Regions:** Anywhere you can place a box. There is no datacentre and no region question, because the hardware is yours.

## HOW YOU RUN HIVE / Tier 2: Colocate it

**Eyebrow:** Dedicated and in-region

**H2:** Colocate it. Your dedicated box, hosted by us in your region.

**What it is:**
We host a Hive box dedicated to you, in-region, on infrastructure under your own jurisdiction. The hardware is not shared with any other customer. We manage the machine so your team does not have to, and residency stays in-region.

**Who it suits:**
Teams that need dedicated, in-region hardware and strong residency, but do not want to run a box themselves. A middle ground when on-prem operations are more than the team wants to carry.

**Sovereignty level (honest):** High, with one honest caveat. The box is dedicated and in-region, under your own jurisdiction, which removes the cross-jurisdiction reach that an out-of-region host would carry. It is still hosted by a third party rather than physically held by you, so it does not match the on-prem tier byte for byte. [LEGAL-REVIEW]

**Ops burden:** Low. We manage the hardware, updates, and uptime. You get a dedicated box without running one.

**Regions:** Canada is the confirmed direction, hosted with an in-region Canadian provider or research-cloud option under Canadian jurisdiction. We also serve the UK and Bangladesh, with in-region providers being confirmed. We will not name a facility until it is locked. [LEGAL-REVIEW]

## HOW YOU RUN HIVE / Tier 3: Shared cloud

**Eyebrow:** Lowest cost, fastest start

**H2:** Shared cloud. Region-locked, fully audited, honestly the lowest tier.

**What it is:**
A shared Hive instance, region-locked, with a full audit trail of every request. It runs only on in-region infrastructure under your own jurisdiction, the same rule as the colocated tier, but capacity is shared across customers rather than dedicated to you.

**Who it suits:**
Teams that are comfortable sharing capacity and want the lowest cost and the fastest start. A practical way to prove value before committing to dedicated or on-prem hardware.

**Sovereignty level (honest):** Lowest of the three, and we will not dress it up. It is region-locked, fully auditable, and runs on infrastructure under your own jurisdiction, but it is not physically isolated and it is not on hardware you control. If physical isolation or on-prem control is your requirement, this is not the tier for you. [LEGAL-REVIEW]

**Ops burden:** None. We run everything. You point your application at it and go.

**Regions:** Region-locked to the region you select, on in-region infrastructure under your own jurisdiction.

## HOW YOU RUN HIVE / Extendable hardware

**Eyebrow:** Capacity you add, not a box you replace

**H2:** When a workload outgrows a box, you add to it.

**Body:**
Hardware needs change as models grow and usage climbs. Hive is built to extend rather than to be thrown out. You start on a single box sized to today's workload. When throughput needs more, you add GPU capacity to that box. When one box is not enough, you scale to multiple nodes. The integration does not change as you grow, so the investment compounds instead of resetting. A box bought today stays useful as the platform and the models advance.

## HOW YOU RUN HIVE / Continuous upgrades

**Eyebrow:** Under the annual licence

**H2:** Your deployment keeps pace with the field.

**Body:**
Owning the hardware does not freeze the software. The annual licence keeps the Hive platform current and adds compatibility with new open-weight models as they ship, so your box runs newer and stronger models over time without a new purchase. You are not buying a fixed snapshot. You are buying a platform that keeps improving on hardware you already own.

## HOW YOU RUN HIVE / Comparison (scannable table)

**Eyebrow:** Side by side

**H2:** The spectrum at a glance.

| | Own it (on-prem) | Colocate it | Shared cloud |
|---|---|---|---|
| **What runs where** | Your box, your building | Your dedicated box, our in-region datacentre | Shared instance, region-locked |
| **Sovereignty** | Highest | High, third-party hosted | Lowest, region-locked and audited |
| **Data isolation** | Physical, on your premises | Dedicated hardware | Shared capacity |
| **Ops burden** | Your team, with our support | We manage it | We manage it |
| **Infrastructure** | Yours | In-region, your jurisdiction | In-region, region-locked |
| **Best for** | Data that cannot leave the building | Dedicated and in-region without running a box | Lowest cost and fastest start |

**Table footnote:** Sovereignty grading describes architectural posture, not a compliance determination. Validate fit for your obligations with your own counsel. [LEGAL-REVIEW]

## HOW YOU RUN HIVE / Closing CTA

**H2:** Not sure which tier fits? We will help you place it.

**Body line:** Tell us about your data and your obligations, and we will recommend the point on the spectrum that fits.

**Primary CTA:** Book a Demo  →  `/book-a-demo`
**Secondary CTA:** See Pricing  →  `/pricing`

---

# PAGE 3: SECURITY & DATA RESIDENCY

Route: `src/pages/security.astro`. Scope: on-prem. Highest claim-risk page. No Pinterest, no cost proof, no named-vendor cost story. Sovereignty stays on-prem scoped and qualified in the same sentence. The court sentence appears only in its permitted verbatim form. The verbatim security disclaimer closes the page. Framework mapping is designed-to-support only, mapped to the right vertical.

## SECURITY / Hero

**Eyebrow:** Security and data residency

**H1:** Residency is where your data sits. Sovereignty is who can be ordered to hand it over.

**Subhead:** Most "data stays in your region" promises solve the first and leave the second untouched. On the on-prem Hive deployment, your data stays inside your own infrastructure, so there is no third party left to compel. [LEGAL-REVIEW]

## SECURITY / Residency is not sovereignty

**Eyebrow:** The distinction that matters

**H2:** A local datacentre run by an out-of-jurisdiction company is still reachable under that company's law.

**Body:**
Data residency means the bytes are physically located in a chosen country. It is a real property and it matters. It is not the same as data sovereignty, which is about which jurisdiction governs the company that operates the infrastructure.

A cloud provider answers to the law of the jurisdiction it is controlled from, wherever its datacentres are. Placing your data in that provider's local region changes where the bytes sit. It does not change who can be ordered to produce them. Sovereignty is reached only when there is no out-of-jurisdiction third party in the path at all, and the strongest version of that is hardware you own and operate yourself. [LEGAL-REVIEW]

**Jurisdiction diagram caption:** With Hive on-prem, the boundary has no outbound path. Without it, your data follows a path out to an external cloud, into a jurisdiction that may not be only yours.

## SECURITY / Two exposure vectors

**Eyebrow:** What on-prem removes

**H2:** Two ways your data leaves your control, and how on-prem closes both.

**Vector card 1 : Cross-jurisdiction reach:**
A cloud provider can be compelled, under the law it answers to, to disclose customer data regardless of where that data is physically stored. The US CLOUD Act is one well-known instance of this general rule. Residency does not defeat it, because the obligation attaches to the provider, not to the datacentre. On-prem Hive removes the provider from the equation, so there is nothing for such an order to reach. [LEGAL-REVIEW]

**Vector card 2 : Litigation discovery:**
Prompts and responses sent to a third-party AI service are records held by another party. They are discoverable, subject to subpoena and preservation, and outside your control once they have left. A US court has already ordered a major AI provider to preserve chat logs. On-prem inference keeps those records inside your own systems, where your existing legal holds and retention policies already govern them. [LEGAL-REVIEW]

> Court sentence usage note for builder: the sentence "A US court has already ordered a major AI provider to preserve chat logs" is the only permitted form. No date, no case name, no provider name, and no implication that it is still in force. Do not edit it.

## SECURITY / How Hive removes the exposure

**Eyebrow:** The architecture

**H2:** When the data never leaves, there is nothing to compel.

**Body intro:**
The exposure exists because a third party holds your data. Hive's on-prem deployment is built so that no third party ever does.

**Architecture points:**
- Open-weight model weights run entirely on your hardware.
- Every inference executes locally.
- No prompt, no response, and no document calls an external API or leaves your network boundary. [LEGAL-REVIEW]

## SECURITY / Designed-for controls, not certifications

**Eyebrow:** Designed-for controls, not certifications

**H2:** Not a compliance certification. A control architecture you can audit.

**Body:**
Hive is designed for on-prem data residency, and customers validate regulatory fit with their own counsel. The controls below are architectural facts you can inspect, not badges we claim.

**Controls:**
- The model runs inside your network.
- The inference path has no outbound call.
- You hold the hardware, the keys, and the logs.

## SECURITY / How on-prem control maps to your obligations

**Eyebrow:** Designed to support, verify with counsel

**H2:** Hive provides the control architecture. Your auditor confirms the fit.

**Body intro:**
The following is general information, not legal advice. Hive is not compliant with, certified for, or ready for any of these frameworks, and we are not pursuing such a certification. What we describe is the on-prem control architecture Hive provides to help you meet obligations you already carry, mapped to the relevant area. Confirm scope and current text against your own auditor or counsel. [LEGAL-REVIEW]

**Finance:**
Hive provides the on-prem control architecture to help financial institutions meet OSFI Guideline B-10 (third-party and outsourcing risk), OSFI Guideline B-13 (technology and cyber risk), SOC 2, and PCI DSS obligations, among others. Keeping data on hardware you control simplifies third-party isolation and audit, and removes the cross-border transfer step from the picture. Validate with your own auditor. [LEGAL-REVIEW]

**Healthcare and privacy:**
Hive provides the on-prem control architecture to help health and privacy obligations under HIPAA (US healthcare), PHIPA (Ontario), and PIPEDA (Canada), among others. Keeping personal health information on hardware the custodian controls keeps it under the custodian's direct accountability, with no external service provider in the data path. HIPAA applies to US healthcare only and does not cover banking. Validate with your own counsel. [LEGAL-REVIEW]

**Legal:**
Professional duties of confidentiality, such as those under the Law Society of Ontario rules in Ontario and SRA rules in the UK, expect client confidences to stay protected. On-prem keeps client matter data inside the firm, with no third-party processor in the chain. Validate with your own counsel. [LEGAL-REVIEW]

**Government and defence:**
For environments that permit no outbound network path, Hive supports air-gapped and offline deployment, so inference runs with no external connectivity at all. Validate suitability for your accreditation requirements with your own authority. [LEGAL-REVIEW]

**Region notes:**
In the UK, FCA and PRA SS2/21 cover material-outsourcing audit rights, documented exit plans, and concentration risk, and UK GDPR covers international-transfer controls. On-prem removes the material-outsourcing relationship and the international-transfer step. In Canada, Quebec Law 25 requires a privacy impact assessment before transferring personal information outside Quebec, and on-prem removes that transfer trigger. Validate current text and scope with your own counsel. [LEGAL-REVIEW]

## SECURITY / Honest Hive Cloud scope boundary

**Eyebrow:** Hive Cloud is different

**H2:** We state the boundary plainly.

**Body:**
The hosted Hive tiers run on third-party infrastructure. They run only on in-region infrastructure under your own jurisdiction, which removes the cross-jurisdiction reach an out-of-region host would carry, but they still do not carry the same data-residency properties as the on-prem edition. If your requirement is that data stays on hardware you control, the on-prem edition is the one that meets it. [LEGAL-REVIEW]

## SECURITY / Disclaimer (verbatim, do not edit)

**Eyebrow:** Disclaimer

**Disclaimer (verbatim):**
Hive's on-prem deployment is designed so that your data does not leave your infrastructure. This is an architectural property, not a compliance certification. Whether this architecture satisfies your organisation's obligations under PIPEDA, HIPAA, or other frameworks is a determination your team must make with qualified legal counsel. S Cubed does not provide legal or compliance advice.

## SECURITY / Closing CTA

**H2:** See it run inside your own network.

**Primary CTA:** Book a Demo  →  `/book-a-demo`
**Secondary CTA:** See Pricing  →  `/pricing`

---

# PAGE 4: PRICING

Route: `src/pages/pricing.astro`. Scope: both. The only route with the Recharts TCO island and the scoped style-src-attr relaxation. No exact dollar figure in any headline. "150x" banned. Approved relative cost lines only. Mandatory assumptions footnote on the TCO band. Verbatim pricing disclaimer.

## PRICING / Hero

**Eyebrow:** Pricing

**H1:** Buy a workstation-class box you own. Stop renting a meter that never stops.

**Subhead:** A third-party AI API charges you per token, and the bill grows with use. An open-weight model you own is a mostly fixed cost up front, then inference is roughly electricity. For sustained daily work, owning is the cheaper position over time.

**Primary CTA:** Book a Demo  →  `/book-a-demo`
**Secondary CTA:** Talk to us about your workload  →  `/book-a-demo`

## PRICING / Ownership versus metered

**Eyebrow:** Two cost shapes

**H2:** A rented meter compounds. An owned box settles.

**Body:**
The two models behave in opposite ways over time. A metered API starts near zero and rises with use, and it rises fastest exactly when AI is working hardest for you. An owned box costs more on day one and then flattens, because once the hardware is paid for, running inference on it is close to the cost of the power it draws.

So the answer depends on how you use AI. For light, occasional, bursty use, a public API is cheap and convenient and there is no reason to own anything. For long-running agents, automations, and sustained heavy daily usage, the metered bill overtakes the owned box and keeps going, while the owned box keeps saving. [LEGAL-REVIEW]

## PRICING / The crossover, explained in words

**Eyebrow:** Where ownership overtakes renting

**H2:** The entry box pays for itself in about seven months, then keeps saving.

**Body:**
Picture the two costs as lines over time. The metered API line starts low and rises with every token. The owned box line starts higher, at the price of the hardware, then barely moves. Early on, renting looks cheaper. Then the two lines cross, and from that point the owned box is ahead and the gap widens every month.

For the entry-tier box on a sustained workload, that crossover lands at about month seven. After that, every month is money the metered API would still be charging and the box no longer is. Measured on output, open-weight inference runs roughly 10x to 30x cheaper than the proprietary APIs, which is why the box settles so quickly.

## PRICING / TCO crossover chart (the one Recharts island)

**Chart headline:** 36-month cost: owned box versus metered API.

**Plain-reading line:** The owned box is a near-flat line. The metered API climbs the whole way. They cross at about month seven for the entry box.

**Mandatory on-face assumptions footnote (verbatim):** Assumes 10M output tokens per month. Power-only opex, excludes labour. Hardware prices approx. June 2026.

**Citation footer:** Modelled 36-month TCO. Strix payback approx month 7, DGX payback approx month 17. Rates as of June 2026. Hosted-API pricing, self-hosting economics differ.

## PRICING / On-prem ladder (per-box tier plus compute add-on plus per-node)

**Eyebrow:** The on-prem ladder

**H2:** Start with one box. Add compute and nodes as you grow.

**Body intro:**
On-prem pricing has three moving parts: the box itself, the compute you add to it, and the nodes you scale out to. You start where your workload is today and step up only when load demands it. The integration does not change as you climb the ladder, so each step builds on the last instead of replacing it.

**Ladder item 1 : The entry box:**
A workstation-class box you own, in the low thousands. It is sized for a small regulated firm running real daily work, and it is the box that pays for itself in about seven months on a sustained workload.
*(On-face hardware note: AMD Strix Halo 128GB class, starts under $2,000, approx June 2026, rising.)*

**Ladder item 2 : The step-up box:**
A higher-throughput box for heavier workloads, still hardware you own.
*(On-face hardware note: NVIDIA DGX Spark, approx $4,699, approx June 2026, rising.)*

**Ladder item 3 : Compute add-on:**
Add GPU capacity to a box as your throughput needs grow, without changing your integration. Priced per unit of compute added. Talk to us for a figure sized to your workload.

**Ladder item 4 : Per-node scale-out:**
When one box is not enough, scale to multiple nodes. Priced per node, so cost tracks capacity. Talk to us for a figure sized to your deployment.

**Hardware capex chart citation:** Published hardware pricing for AMD Strix Halo 128GB class systems and NVIDIA DGX Spark, captured June 2026.

## PRICING / The annual licence

**Eyebrow:** What the licence covers

**H2:** Software, updates, new-model compatibility, and support, on a yearly licence.

**Body:**
On top of the hardware, an annual Hive licence keeps the software current and supported. It covers ongoing software updates, compatibility with new open-weight models as they ship so your box runs newer and stronger models over time, and direct support from the team that builds it. The hardware is a one-time purchase you own. The licence is the yearly cost of keeping it current and supported, so the box you bought keeps improving rather than ageing out.

## PRICING / Hive Cloud tier (no sovereignty or residency language here)

**Eyebrow:** Hive Cloud

**H2:** Not ready to own hardware yet? Start on Hive Cloud.

**Body:**
If you are not ready to buy a box, Hive Cloud runs the same software on a hosted, region-locked instance at a fraction of typical hosted-API pricing. It is the fastest way to start and the lowest up-front cost. Talk to us for a figure sized to your usage.

> Builder note: keep this tier free of sovereignty and data-residency claims per the claim guardrail. The honest residency boundary for hosted tiers lives on the Security page and the How You Run Hive page, not here.

## PRICING / Cost proof (Pinterest, cost section only)

**Eyebrow:** Proof from the field

**Pinterest proof card (verbatim):**
"Pinterest cut AI costs roughly 90 percent and raised accuracy roughly 30 percent by running and customising the open-weight Qwen3-VL model on its own infrastructure."

**Pinterest card mandatory disclaimer (same section, verbatim):** Results reflect Pinterest's specific implementation. Your outcomes will depend on your hardware, model choice, and workload.

**Pinterest card date stamp (mandatory):** as of June 2026

## PRICING / Disclaimer (verbatim, do not edit)

**Eyebrow:** Disclaimer

**Disclaimer (verbatim):**
On-prem pricing covers software licensing and support. Hardware procurement, security hardening, and regulatory validation are the customer's responsibility. Hive Cloud deployments run on third-party infrastructure and do not carry the same data-residency properties as the on-prem edition.

## PRICING / Closing CTA

**H2:** Tell us your workload. We will tell you what it costs to own it.

**Primary CTA:** Book a Demo  →  `/book-a-demo`
**Secondary CTA:** See how you run Hive  →  `/how-you-run-hive`

---

# PAGE 5: USE CASES

Route: `src/pages/use-cases.astro`. Scope: on-prem for the sovereignty framing in each vertical. Each vertical follows pain, then Hive capability, then exposure removed, then outcome, with a concrete worked example. Canada-led, with one US-flavoured and one UK-flavoured example for breadth. Frameworks mapped to the right vertical, designed-to-support only. Pinterest and cost-proof components do not belong on this page.

## USE CASES / Hero

**Eyebrow:** Use cases

**H1:** The work you cannot send to a third-party AI service is the work Hive was built for.

**Subhead:** Privilege, material non-public information, and personal health information do not survive a third-party endpoint. Here is what each looks like when the AI runs inside your own walls instead.

**Primary CTA:** Book a Demo  →  `/book-a-demo`
**Secondary CTA:** See how you run Hive  →  `/how-you-run-hive`

## USE CASES / Finance

**Eyebrow:** Finance and insurance

**H2:** Material non-public information cannot sit on someone else's server.

**Pain:** Internal AI agents in finance touch the most sensitive data a firm holds: deal information, unpublished results, and client positions. Routing that through a third-party API creates an external record subject to discovery and recordkeeping scrutiny, and it sits awkwardly against third-party and outsourcing-risk expectations. [LEGAL-REVIEW]

**Hive capability:** Hive runs internal agents and analysis on open-weight models on the institution's own hardware, with a full local audit trail of every request.

**Exposure removed:** Material non-public information never leaves the institution's boundary, the cross-border transfer step is removed, and there is no external endpoint holding a discoverable copy of the prompt. [LEGAL-REVIEW]

**Worked example (Canada-led):** A Canadian asset manager wants an internal research agent that reads draft deal memos and unpublished earnings models. OSFI Guideline B-10 sets expectations for third-party and outsourcing risk, B-13 for technology and cyber risk, and Quebec Law 25 would treat sending that data outside Quebec as a transfer requiring assessment. Running the agent on a third-party API would place material non-public information into an external provider's systems. With Hive on-prem, the agent runs on the firm's own box, the data stays inside the institution's boundary, the audit trail stays local and inspectable, and there is no cross-border transfer to assess. The firm validates the fit with its own auditor. [LEGAL-REVIEW]

**Outcome line:** Internal agents on your most sensitive data, with the data never leaving your control.

## USE CASES / Healthcare

**Eyebrow:** Healthcare

**H2:** Personal health information stays with the custodian, full stop.

**Pain:** Clinical and administrative AI workflows are valuable, but personal health information sent to an external AI endpoint puts the custodian's accountability at risk and can constitute a breach without an appropriate agreement and controls in place. [LEGAL-REVIEW]

**Hive capability:** Hive runs clinic and hospital workflows on open-weight models on hardware the custodian controls, so personal health information stays in the custodian's own systems.

**Exposure removed:** No personal health information enters an external service provider's data path. The custodian keeps direct control, which is the posture the accountability obligation expects. [LEGAL-REVIEW]

**Worked example (US-flavoured):** A US regional health system wants to draft visit summaries and triage intake notes with AI across several clinics. Under HIPAA, the system stays responsible for how any service provider handles protected health information, and a third-party AI endpoint would place patient records outside its direct control without a business associate agreement and matching controls. With Hive on-prem, the model runs on hardware inside the health system, the summaries are drafted locally, and the patient data never leaves the custodian's own systems. The health system validates HIPAA fit with its own counsel. [LEGAL-REVIEW]

**Outcome line:** The clinical productivity, without ever handing patient data to a third party.

## USE CASES / Legal

**Eyebrow:** Legal

**H2:** Privilege does not survive a third-party processor.

**Pain:** Document review and drafting are exactly where AI helps most, and exactly where the data is most protected. Sending privileged material or client confidences to a third-party AI service puts another party in the chain of custody, which can risk waiver and breach the duty of confidentiality. [LEGAL-REVIEW]

**Hive capability:** Hive runs an open-weight model on a box inside the firm. Lawyers can summarise, review, and search across matter files, contracts, and discovery sets with the model running locally.

**Exposure removed:** No client confidence reaches an external processor. There is no third-party record of the prompt to be subpoenaed and no out-of-jurisdiction provider to be compelled. [LEGAL-REVIEW]

**Worked example (Canada-led):** An Ontario litigation boutique needs to review forty thousand documents in a production. Sending them to a third-party AI tool would route privileged material through an external provider, with the duty of confidentiality under the Law Society of Ontario rules in play and a real risk of privilege waiver. With Hive on-prem, the review model runs on a box in the firm's own server room. The documents never leave the building, the work product stays privileged, and the partner can attest exactly where every byte was processed. The firm confirms the approach with its own counsel. [LEGAL-REVIEW]

**Outcome line:** Faster review, privilege intact, nothing to disclose to anyone.

## USE CASES / Government and defence

**Eyebrow:** Government and defence

**H2:** Some environments allow no outbound path at all.

**Pain:** Public-sector and defence workloads often carry classification, residency, and accreditation requirements that no external AI endpoint can satisfy. For the most sensitive of these, any outbound network connection is itself prohibited, which rules out every hosted AI service by definition. [LEGAL-REVIEW]

**Hive capability:** Hive runs on hardware the organisation controls, and supports air-gapped and offline deployment where no outbound network path is permitted, so inference runs with no external connectivity at all.

**Exposure removed:** With no outbound path, there is no external endpoint, no third-party record, and no cross-jurisdiction reach. The data stays inside the accredited boundary because the architecture provides no way out. [LEGAL-REVIEW]

**Worked example (UK-flavoured):** A UK public-sector body needs AI assistance on sensitive case files held under strict residency and access controls, in an environment where outbound connectivity is not permitted. A hosted AI service is impossible by rule. With Hive deployed air-gapped on hardware inside the accredited boundary, analysts get a capable assistant with no external connection, the case files never leave the environment, and the deployment can be inspected end to end. The body validates accreditation fit with its own authority. [LEGAL-REVIEW]

**Outcome line:** Modern AI inside the boundary, with no path out by design.

## USE CASES / Any team that will not share its data

**Eyebrow:** No third-party cloud

**H2:** When "do not send our data out" is a hard rule, on-prem is the posture that keeps it.

**Pain:** Many organisations are not in a named regulated vertical but still treat their data as something they will not put on someone else's server: trade secrets, source code, R&D, customer records, and competitive plans. For them, every third-party AI tool is a policy violation waiting to happen. [LEGAL-REVIEW]

**Hive capability:** Hive gives them the same modern AI capability their competitors get from public tools, running entirely on their own hardware so it never touches an outside provider.

**Exposure removed:** There is no external endpoint, no third-party record, and no out-of-jurisdiction data path. The hard rule stays intact because the data never leaves. [LEGAL-REVIEW]

**Worked example:** A manufacturer with valuable process IP wants its engineers to use AI on internal design documents and supplier contracts, but company policy forbids sending any internal data to a third party. Public AI tools are off the table by rule. With Hive on-prem, the engineers get a fully capable assistant running on a box in the building, the IP never leaves the network, and the policy is honoured by architecture rather than by trust. [LEGAL-REVIEW]

**Outcome line:** Modern AI for your team, with your no-third-party rule kept by design.

## USE CASES / SMB note

**Eyebrow:** Smaller teams

**H2:** You do not need to be an enterprise to own your AI.

**Body:**
The entry box is workstation-class and priced in the low thousands, which puts owned AI within reach of a small firm, not only a large institution. A boutique law practice, a small clinic, or a lean finance shop can run the same on-prem posture as a much larger organisation, on a single box that pays for itself in about seven months on sustained daily work. As the team grows, the same box extends with added compute and nodes rather than being replaced. [LEGAL-REVIEW]

**Link CTA:** See pricing  →  `/pricing`

## USE CASES / Competitive one-liner

**H2 / pull quote:** Glean, Writer, Cohere North, and Copilot route every query through their cloud. Hive runs in yours.

## USE CASES / Closing CTA

**H2:** Find your use case running inside your own network.

**Body line:** Book a demo and we will show Hive running locally on the kind of work your team does every day.

**Primary CTA:** Book a Demo  →  `/book-a-demo`
**Secondary CTA:** See Pricing  →  `/pricing`

---

# APPENDIX: GLOBAL MICROCOPY

**Nav labels:** Home, How You Run Hive, Security, Pricing, Use Cases, Book a Demo
**Footer entity:** S Cubed Technology Ltd.
**Footer tagline:** Your AI, on your hardware.
**Primary CTA (site-wide):** Book a Demo  →  `/book-a-demo`
**Secondary CTA (site-wide):** See Pricing  →  `/pricing`

**General-info microcopy (reusable, for any regulatory mention):** General information, not legal advice. Validate fit for your obligations with your own counsel.

**Designed-to-support microcopy (reusable, for any framework mention):** Hive provides the on-prem control architecture to help you meet this obligation. It is not a certification. Validate with your own auditor.

`[NATIONALITY-SLOT]` Footer nationality line to add once S Cubed is Canadian-registered: "A Canadian company. Independently held." Do not publish until registration is confirmed.

---

# BUILDER COMPLIANCE CHECKLIST (quick scan before drop-in)

- [ ] H1 on Home is verbatim and unchanged: "Your AI runs on your hardware. Not ours. Not anyone's."
- [ ] "sovereign" / "sovereignty" is scoped to the on-prem box in the same sentence, every time.
- [ ] Court sentence appears only as "A US court has already ordered a major AI provider to preserve chat logs", with no date, case, or provider, and no implication it is still in force.
- [ ] No framework is called compliant, certified, or ready. Compliance language is designed-to-support only.
- [ ] No certification is described as in progress or planned.
- [ ] Frameworks are mapped to the right vertical: HIPAA, PHIPA, PIPEDA to health and privacy (HIPAA is US healthcare only, never banking); OSFI B-10 and B-13, SOC 2, PCI DSS to finance.
- [ ] The US is framed neutrally as one illustration of a general principle, never as the villain. Hosted infrastructure is "in-region, under your own jurisdiction".
- [ ] Pinterest appears only in cost sections (Home cost band, Pricing), never on Security, never beside a sovereignty claim, always with its results-vary disclaimer and date stamp.
- [ ] Cost-wedge carries the exact on-face footnote "~10x to ~30x cheaper". "150x" appears nowhere, including comments.
- [ ] TCO band carries the verbatim assumptions footnote.
- [ ] Pricing and Security carry their verbatim disclaimers, unedited.
- [ ] Hive Cloud / hosted tiers carry no sovereignty or data-residency claim; honest boundary language only.
- [ ] Continuous-upgrade and extendable-hardware mechanisms appear on How You Run Hive and Pricing, echoed on Home.
- [ ] Verticals on Home and Use Cases cover finance, healthcare, government and defence, legal, and any sensitive-data team. Use Cases includes one US-flavoured and one UK-flavoured worked example.
- [ ] No named Hive customer anywhere.
- [ ] "Airbnb" appears nowhere.
- [ ] Canadian English, no dash punctuation between clauses.
- [ ] `[NATIONALITY-SLOT]` lines remain unpublished until Canadian registration is confirmed.

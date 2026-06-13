# Hive Sovereign Marketing Site — Build Specification

This is the single source of truth for the Hive sovereign marketing site. Page
builders read ONLY this document. Everything needed to build a page lives here:
positioning, locked copy, the proof spine, chart data, the claim guardrail, the
verbatim disclaimers, the design system, and the build process.

Canadian English throughout. No dash punctuation between clauses in any prose
(use commas, colons, parentheses, or separate sentences). Hyphens inside
compound words are fine.

---

## 0. Entity, domains, audience

- Legal entity: **S Cubed**. (Pre-publish gate confirms the exact registered
  legal name before launch.)
- This site: **hive.scubed.co** (rest of world).
- Bangladesh site is separate: **hive.scubed.com.bd**. It is not part of this
  project. The BD marketing site lives in the repo at `website/bd/`.
- Core promise: **Your AI runs on your hardware. Your data never moves.**
- Two pillars: **sovereignty** and **economics**.
- Tone: confident, plain, founder-direct, outcome-led. Credible to both a
  compliance officer and a non-technical SMB owner at the same time.

---

## 1. Design system (Tasks C, D)

### 1.1 Colour tokens

Defined in `src/styles/tokens.css` (`:root` + Tailwind 4 `@theme`) and mirrored
in `tailwind.config.ts`.

Surfaces:

- `--bg` `#0a0e16`
- `--bg-raised` `#11161f`
- `--bg-card` `#141b26`

Borders:

- `--border` `#1e2733`
- `--border-strong` `#2a3543`

Text:

- `--text` `#f5f7fa`
- `--text-muted` `#aeb6c2`
- `--text-faint` `#828b99`

Accents:

- `--gold` `#f0b429` — scarce, **one per viewport**.
- `--teal` `#2dd4bf` — verified, safe, sovereign.
- `--red` `#f04438` — **RISK framing only**.

Competitor baseline:

- `--grey` `#6b7585` — AA verified at 3.71:1 on `--bg-card`. Do **not** use
  `#5b6472`; it fails contrast.

### 1.2 Geometry

- Radius: `14px`.
- Max content width: `1140px` (`.shell` / `max-w-shell`).
- Section rhythm: `120px` (`spacing.section`, `.py-section`).

### 1.3 Typography (self-hosted Fontsource, latin woff2, font-display swap)

- **Fraunces 600** — H1 only. `clamp(44px, 5.5vw, 60px)`.
- **Inter 400 / 500 / 600** — all UI, headings (H2 down), body.
- **Geist Mono 400 / 500**, tabular (`tnum`) — ALL numerals and all code.
- Preload Fraunces 600 and Inter 400 only (done in `Base.astro`). Other weights
  load on demand.
- In markup, wrap numerals in `<span data-numeric>` or apply `.font-mono` so the
  tabular mono face is used.

---

## 2. Content collections and data governance (Task E)

- Page content: MDX under `src/content/pages/`. Schema and loaders in
  `src/content.config.ts`.
- Chart data: `src/data/charts.json`, validated by the `charts` collection.
- A chart with a missing or empty `source` fails the build (zod `.refine`).
- A chart with `verified: false` fails the PRODUCTION build unless
  `ALLOW_UNVERIFIED=1` is set. Dev (`astro dev`) always allows it.
- Every `<Claim id="...">` in MDX must have a matching `sources[].claim_id` in
  that page's frontmatter, or the build fails (content-lint integration).

### 2.1 MDX compliance lints (ERROR, fail build)

Enforced by `src/integrations/content-lint.ts`:

1. **Sovereignty wording** (data never leaves, stays on your hardware, no data
   leaves, data residency, data sovereignty, inside the network boundary, etc.)
   may appear only where onprem scope is in play: `scope: onprem` or
   `scope: both`. A `scope: cloud` page must never carry sovereignty or no-egress
   language.
2. **Pinterest proof point** may appear ONLY in a cost-scoped section. Mark the
   section with an HTML comment containing `cost-section`. Never on the Security
   page, never adjacent to a sovereignty claim. The lint trips on the actual
   claim (the name plus a signature figure such as Qwen3-VL, ~90 percent cost,
   or ~30 percent accuracy), so a neutral mention does not false-positive.
3. **Banned strings**: `"150x"` and `"up to 150x"` anywhere, including comments.

---

## 3. Content Security Policy (Task F)

- Baseline (every route): `default-src 'self'`; `script-src 'self'` (plus the
  build-time hashes Astro injects, NO `'unsafe-inline'`); `style-src 'self'`
  (plus hashes); `font-src 'self'`; `img-src 'self' data:`. Plus `base-uri`,
  `form-action`, `frame-ancestors 'none'`, `object-src 'none'`.
- The ONLY relaxation: `style-src-attr 'unsafe-inline'` scoped to the `/pricing`
  route alone, because Recharts writes inline style attributes on SVG nodes.
- Astro's built-in CSP (`security.csp` in `astro.config.mjs`) handles script and
  style hashing. `public/_headers` carries the transport-layer baseline and the
  per-route `/pricing` relaxation.

---

## 4. Layout shell (Task G)

- `src/layouts/Base.astro`: `html lang="en"`, meta, font preloads, skip-link to
  `#main`, gold focus ring (>= 3:1), `prefers-reduced-motion` honoured, target
  sizes >= 24px (`.tap-target`).
- Nav: Home, Security & Data Residency, Pricing, Use Cases, plus **Book a Demo**
  (gold). Defined in `src/components/Nav.astro`.
- Footer: entity **S Cubed**, nav links, country switcher anchors to `?geo=bd`
  and `?geo=co` (a Worker handles redirect later). Defined in
  `src/components/Footer.astro`.
- Background: faint hex-lattice motif (~4% opacity), NOT the BD honeycomb.

---

## 5. Chart infrastructure (Task H)

Components in `src/components/charts/`:

- `ChartFrame.astro` — editorial frame: headline / chart / plain-reading line /
  citation footer / optional `compliance-footnote` slot. A chart with no
  citation renders a visibly broken state. Never fake a citation.
- `SovereigntyChart.astro` — static SVG two-series (teal sovereign vs red
  exposed). Home hero chart.
- `StaticBar.astro` — static SVG horizontal bars. Used for cost-wedge,
  quality-parity, hardware capex. Zero-baseline, direct labels, no legend box,
  one gold annotation max, horizontal gridlines `#1e2733` only, Geist Mono
  tabular ticks.
- `JurisdictionDiagram.astro` — static SVG. Teal boundary with zero outbound
  arrows; separate red "without Hive" external-cloud path.
- `CalloutCard.astro` — proof-point cards (Perplexity, Pinterest).
- `TcoCrossover.tsx` — the ONE Recharts island (`client:visible`), reserved for
  the `/pricing` TCO crossover BAND chart ONLY.
- `colors.ts` — token-to-hex mapping. Cost charts: teal (Hive/open) vs grey
  (competitor) + gold callout. Risk charts: teal (sovereign) vs red (exposed).
  Competitor series is GREY, never red.

---

## 6. Positioning

Core promise: **Your AI runs on your hardware. Your data never moves.**
Pillars: sovereignty + economics. Entity S Cubed. Site hive.scubed.co (rest of
world); BD site separate at hive.scubed.com.bd. Tone confident, plain,
founder-direct, outcome-led, credible to a compliance officer and a
non-technical SMB owner. Canadian English, no dash punctuation.

---

## 7. Hero (Home) — LOCKED

- **H1**: "Your AI runs on your hardware. Not ours. Not anyone's." (Fraunces,
  gold accent on the word "anyone's").
- Subhead: THREE teal-tick benefit fragments:
  1. "Open-weight models on your own servers."
  2. "No data leaves your building."
  3. "No usage-based vendor bill."
- Faint scope clarifier below the ticks (no tick): "Self-hosted deployment. Your
  hardware, your network, your data."
- CTAs: "Book a Demo" (gold filled) + "See Pricing" (ghost), both above the
  fold.
- Layout: split-evidence, copy left, hero chart right.
- HERO CHART: SOVEREIGNTY TWO-SERIES (static SVG): teal = data stays on your
  hardware, red = cloud AI data exits your building (CLOUD Act jurisdiction).
  Architectural fact, no projections.
- The cost-wedge bar lives BELOW the fold, never in the hero.

---

## 8. Proof spine (compliance-cleared)

Airbnb is OMITTED. No named customer.

1. **Architecture**: "Open-weight model weights run on your hardware. Every
   inference executes locally. No prompt, no response, no document ever calls an
   external API or leaves your network boundary."
2. **Perplexity**: "Perplexity runs DeepSeek R1 on its own servers in US and EU
   data centres. User data never leaves Western servers." Render with "as of
   January 2025".
3. **Pinterest** (COST SECTION ONLY, never adjacent to a sovereignty claim):
   "Pinterest cut AI costs roughly 90 percent and raised accuracy roughly 30
   percent by running and customising the open-weight Qwen3-VL model on its own
   infrastructure." Same-section disclaimer: "Results reflect Pinterest's
   specific implementation. Your outcomes will depend on your hardware, model
   choice, and workload."
4. **Regulatory category** (general information, not advice): finance (GLBA, FTC
   Safeguards), healthcare (HIPAA), legal (privilege) cannot route sensitive
   data through an external AI endpoint; self-hosted open weights keep every
   inference inside the network boundary.

---

## 9. Chart data

All charts are `verified: false` until a pre-publish re-pull. Every chart shows
"rates as of June 2026" and "hosted-API pricing, self-hosting economics differ".
Competitor series is GREY, never red. Data lives in `src/data/charts.json`.

- **cost-wedge** (static bar, below Home fold + Pricing): open output ~$0.196 to
  $1.92 /1M vs proprietary ~$12 to $30 /1M. Callout EXACTLY "~10x to ~30x
  cheaper". "150x" is BANNED.
- **quality-parity** (static bar): Artificial Analysis Intelligence Index v4.0,
  best open 54 vs best closed 60. Claim "best open within roughly 10 percent of
  best closed". On-face footnotes: "Rankings shift frequently. Verify current
  standings at artificialanalysis.ai before citing." plus "captured [DATE]".
- **onprem-hardware** (static capex bars): ladder, AMD Strix Halo 128GB entry is
  PRIMARY "starts under $2,000", NVIDIA DGX Spark step-up "approximately
  $4,699", label "approx June 2026, rising". No exact dollar figure in any
  headline (say "low thousands" or "a workstation-class box you own").
- **tco-crossover** (the ONE Recharts island, /pricing): 36-month, BAND not
  point, assumes 10M output tokens/month (print on face: "Assumes 10M output
  tokens per month. Power-only opex, excludes labour. Hardware prices approx.
  June 2026."). Strix payback ~month 7, DGX payback ~month 17. Month-36 totals
  ~$2,391 (Strix) / ~$5,261 (DGX) vs ~$10,800 cloud API. Gold breakeven
  annotation. Pricing headline-safe framing allowed: "The entry box pays for
  itself in about 7 months, then keeps saving."

---

## 10. Page to content map

### Home

Hero (sovereignty two-series) + proof spine strip + cost-wedge bar (below fold,
distinct section band) + dual delivery teaser (on-prem box vs Hive Cloud) +
competitive one-liner (Glean, Writer, Cohere North, Copilot route every query
through their cloud; Hive runs in yours) + dual CTA.

**COMPLIANCE PLACEMENT RULE**: cost-proof components (Pinterest, cost-wedge) and
residency-risk components (sovereignty two-series) NEVER share a section band.

### Security & Data Residency (scope: onprem)

Jurisdiction diagram (static SVG, teal boundary with zero outbound arrows, red
"without Hive" external-cloud path) + two exposure vectors (US CLOUD Act compels
US-owned providers regardless of physical data location; litigation discovery) +
the generic court sentence + how Hive removes it + designed-for controls (NOT
certifications) + honest Hive-Cloud scope boundary + SECURITY DISCLAIMER (section
12). No Pinterest here.

### Pricing (scope: both)

On-prem ladder (Strix entry / DGX step-up; annual licence covers updates, new
open-model compatibility, and support) vs Hive Cloud tier (regional, a fraction
of US price, NO sovereignty language) + TCO crossover Recharts island + Pinterest
cost proof + PRICING DISCLAIMER (section 12).

### Use Cases

Four verticals: law, finance, healthcare, SMB. Each: pain -> Hive capability ->
exposure removed. Outcome-led.

---

## 11. Claim guardrail (ENFORCE)

### RED (never)

- A framework named as "compliant" / "certified" / "ready" (SOC2, HIPAA,
  PIPEDA, GDPR).
- "sovereign" used unqualified without on-prem scope in the same sentence.
- A court order with any date, case, or provider, or implying it is still in
  force.
- A no-egress claim on any hosted provider API.
- "your region" data-residency language on Hive Cloud.
- "150x" anywhere.
- Airbnb in any form.
- Pinterest on the Security page or adjacent to a sovereignty claim.
- Any unverified named-company proof point.

### GREEN

- On-prem-scoped sovereignty: "Self-hosted deployment. Your hardware, your
  network, your data."
- "designed for on-prem data residency, customers validate regulatory fit with
  their own counsel".
- The exact court sentence: "A US court has already ordered a major AI provider
  to preserve chat logs." (no date, no name).
- "open model weights run entirely on your hardware, inference never calls an
  external API" (self-hosted only).
- "Not a compliance certification. A control architecture you can audit."

---

## 12. Disclaimers (VERBATIM)

### Security page

"Hive's on-prem deployment is designed so that your data does not leave your
infrastructure. This is an architectural property, not a compliance
certification. Whether this architecture satisfies your organisation's
obligations under PIPEDA, HIPAA, or other frameworks is a determination your team
must make with qualified legal counsel. S Cubed does not provide legal or
compliance advice."

### Pricing page

"On-prem pricing covers software licensing and support. Hardware procurement,
security hardening, and regulatory validation are the customer's responsibility.
Hive Cloud deployments run on third-party infrastructure and do not carry the
same data-residency properties as the on-prem edition."

---

## 13. Build skill mandate (per page)

Each page is built in this order:

1. `superpowers:brainstorming` — explore intent and structure.
2. `ui-ux-pro-max` skill — plan and design.
3. `frontend-design` skill — component build.
4. `ui-ux-pro-max` review + WCAG 2.2 AA pass.

---

## 14. Pre-publish gate

Before launch, all of the following must pass:

- Citation gate runs without `ALLOW_UNVERIFIED` (all charts `verified: true`).
- Compliance MDX lints pass.
- CSP validation.
- Lighthouse + WCAG AA.
- Both Pages projects build clean.
- Link check.
- Data 5-item re-verify (cost-wedge, quality-parity, onprem-hardware,
  tco-crossover, Perplexity).
- S Cubed exact registered legal name confirmed.
- Compliance manual sign-off.

---

## 15. Repo layout

```
website/
  bd/                 Bangladesh marketing site (separate, hive.scubed.com.bd)
  sovereign/          this Astro project (hive.scubed.co)
    astro.config.mjs  output static, integrations mdx + react + content-lint,
                      Tailwind 4 via @tailwindcss/vite, built-in CSP
    tailwind.config.ts
    tsconfig.json
    src/
      content.config.ts        pages + charts collections, zod, build guards
      content/pages/*.mdx      page content (home, security, pricing, use-cases)
      data/charts.json         chart data (all verified:false pre-publish)
      integrations/content-lint.ts   compliance MDX lints
      layouts/Base.astro
      components/Nav.astro, Footer.astro, Claim.astro
      components/charts/*       chart components (1 Recharts island, rest static)
      pages/*.astro            route stubs (index, security, pricing, use-cases)
      styles/tokens.css, global.css
    public/_headers            CSP baseline + /pricing scoped relaxation
```

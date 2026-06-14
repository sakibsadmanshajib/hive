# Sovereign Site SEO, GEO/AEO, and Performance Spec

Site: hive.scubed.co (Astro 6.4.6, output static, Cloudflare hosted)
Org: S Cubed Technology Ltd. Product brand: Hive. Title pattern: `${title} | Hive`.

This spec describes the SEO, AI-search, and performance work for the sovereign
marketing site. The standalone assets (robots.txt, llms.txt, StructuredData.astro)
are already created. Everything in the "Changes the builder must apply" section is
the page-rebuild owner's job, not this spec author's.

Copy rules honoured throughout: never pair "compliant" with a framework name,
never name a specific cloud provider, keep court-order language generic, Canadian
English, no dash punctuation between clauses, do not invent metrics, never attach
sovereignty or data-residency claims to Hive Cloud.

---

## 1. Per-page title and meta description

Rule for titles: keep the full string at or under 60 characters including the
" | Hive" suffix (8 characters). That leaves about 52 characters for the page
title text. One H1 per page should echo the title's intent without being identical.

Rule for descriptions: 120 to 160 characters. Lead with the direct answer
(answer-first), name the entity (Hive or S Cubed Technology Ltd.) where natural,
end with a reason to click. No dash punctuation.

Recommended drafts (final wording is the owner's call; these satisfy the rules):

### Home `/`
- Title: `Hive | Hive` reads badly, so use the value prop as the title text.
  Recommended title: `Sovereign On-Prem AI | Hive` (27 chars)
- Description (148 chars):
  `Your AI runs on your hardware, not ours. Hive runs open-weight models on a box you own, so prompts and documents never leave your building.`

### Security and Data Residency `/security`
- Recommended title: `Security and Data Residency | Hive` (34 chars)
- Description (150 chars):
  `How Hive keeps AI on your own hardware inside your network boundary, with no external endpoint to subpoena and no data leaving your building.`

### Pricing `/pricing`
- Recommended title: `Pricing | Hive` (14 chars). If room is wanted for keywords,
  `Hive Pricing and Payback | Hive` (31 chars).
- Description (151 chars):
  `What it costs to own a Hive box plus the annual licence, the payback period, and how owning the box compares to a metered cloud API over time.`

### Use Cases `/use-cases`
- Recommended title: `Use Cases | Hive` (16 chars). Keyword variant:
  `AI Use Cases for Regulated Work | Hive` (38 chars).
- Description (156 chars):
  `Law, finance, healthcare, and small business, each mapped to a Hive capability and the exposure or cost it removes. On-prem for regulated work.`

### How You Run Hive `/how-you-run-hive` (coming soon)
- Recommended title: `How You Run Hive | Hive` (23 chars).
- Description (149 chars):
  `How a Hive box is deployed and operated inside your own network, from install to day-to-day use. Detailed walkthrough coming soon.`

Note: the meta description currently flows from Base.astro Props.description, which
the page files already pass. No change needed beyond using these strings.

---

## 2. Canonical

Self-canonical is already implemented in Base.astro via
`<link rel="canonical" href={Astro.url.href} />`. Keep it as is. Every page
canonicals to itself, which is correct for the .co global site. Do not change it.

---

## 3. Open Graph and Twitter card tags (builder must add to Base.astro head)

Base.astro currently emits og:title, og:description, og:type=website. It is
MISSING the rest. The builder must add the following to Base.astro head and grow
the Props interface.

Props growth (Base.astro):
- Add optional `image?: string` (path to a page OG image, absolute or root-relative).
- Add optional `ogType?: string` (defaults to `"website"`).
- Compute an absolute image URL at build, e.g.
  `const ogImage = new URL(image ?? '/og/default.png', Astro.site).href;`
  (Astro.site is set to https://hive.scubed.co, so this yields an absolute URL.)

Tags to add (alongside the existing og:title / og:description / og:type):
```
<meta property="og:url" content={Astro.url.href} />
<meta property="og:site_name" content="Hive" />
<meta property="og:image" content={ogImage} />
<meta property="og:image:width" content="1200" />
<meta property="og:image:height" content="630" />
<meta name="twitter:card" content="summary_large_image" />
<meta name="twitter:title" content={fullTitle} />
<meta name="twitter:description" content={description} />
<meta name="twitter:image" content={ogImage} />
```
Replace the existing `og:type` line with `content={ogType}` once the prop exists.

OG image policy: one 1200x630 PNG per page is ideal, but a single shared default
at `/public/og/default.png` is acceptable for v1. The image is decorative for
crawlers (no alt is read from it), but the declared width and height MUST match
the real file so cards render without reflow. CSP impact: none. The image is a
same-origin asset and img-src already allows 'self'.

Robots meta: leave default (index, follow). No tag needed. The coming-soon
How You Run Hive page may carry `<meta name="robots" content="noindex">` until it
has real content, then remove it. This is optional and the owner's call.

---

## 4. Semantic heading rules

- Exactly one H1 per page. The H1 is the page's main claim. Current pages already
  do this (e.g. use-cases has a single h1#usecases-title).
- H2 for top-level sections, H3 only nested under an H2. Never skip a level for
  visual sizing; size with CSS, not heading rank.
- Section landmarks already use aria-labelledby pointing at the section heading.
  Keep that pattern on every new section.

---

## 5. Image and SVG alt-text requirements

- Decorative motifs (the hex-lattice background) stay `aria-hidden="true"`. Already done.
- Inline decorative SVG icons get `aria-hidden="true"` and no text alternative.
- Meaningful diagrams (the JurisdictionDiagram on Security) carry a descriptive
  accessible name. The component already accepts an `ariaLabel` prop; ensure the
  passed label describes what the diagram shows, e.g. "With Hive every inference
  stays inside your network boundary; without Hive data leaves to an external
  cloud under foreign jurisdiction." Prefer role="img" plus aria-label, or an
  SVG `<title>` as the first child, so screen readers announce it.
- Any future raster image needs descriptive `alt` if it carries meaning, or
  `alt=""` if purely decorative, plus explicit width and height (see CLS below).
- The OG image is decorative for crawlers, but its declared dimensions
  (1200x630) must match the real asset.

---

## 6. JSON-LD placement (via StructuredData.astro)

The component at `src/components/StructuredData.astro` emits the schema. The
builder drops `<StructuredData ... />` into Base.astro head and passes per-page
props. Suggested mapping:

| Page | Props to pass |
|------|---------------|
| Home `/` | `organization website service` plus `faq` (FAQ Q&A lives here or wherever the on-page FAQ actually renders) |
| Security `/security` | `breadcrumbs={[{name:'Home',url:'https://hive.scubed.co/'},{name:'Security and Data Residency',url:'https://hive.scubed.co/security'}]}` |
| Pricing `/pricing` | `service` (if not already on home) plus the same breadcrumb pattern |
| Use Cases `/use-cases` | breadcrumb pattern |
| How You Run Hive | breadcrumb pattern (once published) |

Rules:
- Organization and WebSite belong on the home page only (site-wide identity).
- Service describes the Hive product; put it on home OR pricing, not duplicated.
- FAQPage goes ONLY where the matching Q&A is actually visible on the page. The
  component's five Q&A entries are drawn from the sovereignty and cost story. If
  the on-page FAQ differs, update the component's `faqNode` so structured data
  matches visible content (Google requires this; mismatched FAQ markup is a
  manual-action risk).
- BreadcrumbList on interior pages, built from the trail prop in order.

CSP interaction (primary expectation plus fallback):
- PRIMARY: `<script type="application/ld+json">` is a non-executable data block.
  CSP script-src gates executable script, so ld+json is generally not enforced
  under it. The content is also fully static at build time (props only, no
  per-request values), so any hash Astro emits is deterministic and added to the
  policy automatically. Expectation: JSON-LD does NOT break CSP.
- VERIFY against astro ^6.4.6: build, inspect the emitted
  `<meta http-equiv="content-security-policy">`, and load each page checking the
  browser console for a CSP violation on the ld+json block.
- FALLBACK if a violation appears: add the printed sha256 hash to `script-src`
  for the affected routes in `public/_headers` (the same file that carries the
  pricing route's style-src-attr relaxation). Do not add 'unsafe-inline'. Because
  ld+json is not executed, the security posture is unchanged either way.

---

## 7. hreflang

Already implemented in Base.astro: self en, en-CA, x-default on the .co side, and
bn-BD plus en-BD pointing at the .com.bd sibling. Keep as is. Two follow-ups:
- The Bangladesh sibling (.com.bd) MUST mirror reciprocal hreflang back to .co
  (en / en-CA / x-default to the .co URLs), or search engines will distrust the
  cluster. This is a task for the BD site, noted here for tracking.
- NEVER make the geo redirect a 301. Base.astro already carries this warning. A
  301 would collapse one site's index into the other. Keep it a non-permanent
  hint at the Worker layer.

---

## 8. Sitemap (builder must add @astrojs/sitemap)

robots.txt already references `https://hive.scubed.co/sitemap-index.xml`, which is
exactly the index path @astrojs/sitemap produces. The builder adds the
integration. Do NOT apply this here.

Install: `npm i @astrojs/sitemap`

astro.config.mjs change (import plus integration entry):
```
import sitemap from '@astrojs/sitemap';
// ...
integrations: [
  mdx(),
  react(),
  sitemap(),
  contentLint(),
],
```
CSP impact: none. The sitemap is static XML served from the same origin and is
not subject to CSP. It produces /sitemap-index.xml plus /sitemap-0.xml. The
coming-soon How You Run Hive page will appear in the sitemap once it is a built
route; if it should be hidden until launch, exclude it via the sitemap `filter`
option or keep it noindex.

---

## 9. Core Web Vitals targets and Astro tactics

Targets: LCP < 2.5s, INP < 200ms, CLS < 0.1.

Tactics specific to this site:
- Keep zero client JS on content pages. Only the pricing page ships an island
  (Recharts). Keep that island `client:visible` (or `client:idle`); never
  `client:load`, so it never blocks LCP or INP on first paint.
- Fonts: two woff2 faces (Fraunces 600, Inter 400) are already preloaded with
  crossorigin, self-hosted, font-display swap via Fontsource. Do not add more
  preloads; each preload competes with LCP. Other weights load on demand.
- CLS: set explicit width and height on every raster image and on any embedded
  media so the box is reserved before load. Inline SVGs already render at build
  with intrinsic size. The OG image dimensions must match the file.
- Inline critical SVGs at build (already done via `?raw` + set:html on home).
- No render-blocking third-party scripts. Keep default-src 'self'; do not add
  external script origins.
- For any future images, use Astro's `<Image>` component or hard-coded
  width/height attributes. Avoid layout-shifting late-loading content.
- Syntax highlighting is disabled in astro.config (Shiki inline styles fight CSP
  and there are no code blocks). Keep it off.

---

## 10. AEO/GEO checklist (AI answer engines)

- Answer-first: lead each page and each section with the direct answer, then
  support it. AI engines extract the lead sentence.
- Consistent entity naming: write "S Cubed Technology Ltd." and "Hive" the same
  way every time so the entities resolve cleanly. Avoid "Fundmore", abbreviations,
  or variant spellings.
- Structured data: the schemas in section 6 give engines machine-readable entity,
  product, and FAQ facts.
- llms.txt present at /llms.txt: a concise markdown map of the site and the core
  value for LLM crawlers. Already created.
- FAQ blocks must match the on-page FAQ exactly (and the FAQPage schema).
- Concise scannable headings, short paragraphs, plain claims.
- Factual claims keep the existing `<Claim id>` citation pattern tied to the
  page's sources[] in the matching .mdx, so claims stay backed and the build lint
  enforces it.
- Do NOT attach sovereignty or data-residency language to Hive Cloud anywhere,
  including FAQ and structured data. Hive Cloud is cost only.

---

## 11. Changes the builder must apply (NOT done by this spec)

Each item below is the page-rebuild owner's job. The standalone assets
(robots.txt, llms.txt, StructuredData.astro, this spec) are already created.

- (a) Add the OG and Twitter tags from section 3 to Base.astro head, and grow
  Base.astro Props with optional `image?: string` and `ogType?: string`, plus the
  `ogImage` absolute-URL computation. Update the existing og:type line to use the
  prop.
- (b) Drop `<StructuredData ... />` into Base.astro head with per-page props per
  the table in section 6. Pass props through from each page (e.g. a `structured`
  prop object or per-page flags), or place StructuredData on each page directly.
- (c) Add `@astrojs/sitemap` to astro.config.mjs using the snippet in section 8,
  and install the package. CSP impact none.
- (d) Create a 1200x630 OG image at `/public/og/default.png` (and optionally one
  per page under /public/og/). Declared dimensions must match the file.
- (e) Build and verify the ld+json blocks pass CSP (section 6). If a violation
  appears, add the printed sha256 hash to script-src for the affected routes in
  public/_headers. Do not add 'unsafe-inline'.

---

## 12. Top SEO gaps found (current state)

1. No sitemap. @astrojs/sitemap is not installed and not in astro.config; robots
   now references the index path but nothing produces it yet.
2. No Open Graph image, no og:url, og:site_name, and no Twitter card tags. Social
   and AI link previews render bare.
3. No structured data (JSON-LD) on any page. Now available via StructuredData.astro
   but not yet wired into Base.astro.
4. No robots.txt and no llms.txt existed. Both now created.
5. No favicon or OG asset directory in /public. A favicon and a 1200x630 OG image
   should be added (favicon is a minor polish gap, OG image is the blocker for
   shareable previews).

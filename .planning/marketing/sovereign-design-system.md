# Hive Sovereign Site: Visual and Media Design System

Art-directed institutional minimalism. Restraint with craft, not emptiness. This document is the design spec for the sovereign marketing site (hive.scubed.co, S Cubed entity). It refines the existing light-institutional palette, defines a hero motif and three diagrams (all shipped as real SVG assets), and specifies imagery, motion, and component layout so pages read as designed rather than templated.

Companion reference: the live tokens are in `src/styles/tokens.css` and `src/styles/global.css`. The build rules (CSP, chart colour discipline, locked hero, Canadian English, no dash punctuation) are in `src/SPEC.md`. This document does not override SPEC.md; it elevates the visual layer inside those rails.

Status of conflicting inputs: the brief mentioned a forest accent plus a legacy teal chart palette. The current repository has already resolved this. `tokens.css` is light-institutional with a single deep forest accent (`#0d3b2e`), and the legacy `--gold` / `--teal` token names are repointed to that one forest value. The dead teal/gold hues in `src/components/charts/colors.ts` (`#2dd4bf`, `#f0b429`) are a dark-theme leftover and are not in use on the light site. This system keeps the single-forest discipline and treats red strictly as residency-risk, per SPEC.

---

## 0. Design principles (the five rules that keep it from drifting)

1. One accent only. Deep forest `#0d3b2e`. At most one forest highlight per viewport. If a second forest element competes for attention, demote one to ink or muted.
2. Hairlines over shadows. Structure comes from 1px borders, generous whitespace, and a tight type scale, not from elevation. Shadows are reserved for one element class (raised cards on hover) and stay almost invisible.
3. Ink does the work. The near-black ink (`#0d1117`) carries hierarchy through weight and size. Colour is not a hierarchy tool here; size, weight, and space are.
4. Red is a loaded word. Red (`#f04438`) appears only to mark residency risk (data leaving the boundary). Never for emphasis, never for a competitor series, never decoratively. Competitor and cloud series are grey.
5. Craft fills the space. Minimal does not mean blank. The space is filled with drafted line work (the hex lattice, the boundary motif, the diagrams), precise vertical rhythm, and editorial typography. Whitespace is composed, not left over.

---

## 1. Design tokens (refined)

Keep every existing token value. The block below is additive: it layers a full spacing scale, type scale, line-height set, motion tokens, two new surfaces, and an elevation token onto what already exists. Nothing here changes a colour already in `tokens.css`.

### 1.1 Ready-to-paste token additions

Paste these new custom properties into the `:root` block of `src/styles/tokens.css`, after the existing geometry tokens. They do not collide with any existing name.

```css
/* ---- additive refinements (art-directed minimalism) ---- */

/* surfaces: two faint additions for quiet depth without new hues */
--bg-forest-tint: #f1f4f2;   /* off-white pulled 4% toward forest; band wash */
--bg-sunken: #efece6;        /* slightly deeper than --bg-raised; quote/aside */

/* accent tints derived from the single forest, for fills behind accent text */
--accent-wash: #e9efeb;      /* 8% forest on paper; pill / chip background */
--accent-line: #c3d3cb;      /* forest hairline at low contrast; inset rules */

/* residency-risk red, single source (mirrors charts/colors.ts red) */
--risk: #f04438;
--risk-wash: #fdecea;        /* only behind a risk marker, never large areas */

/* spacing scale, 4px base, used for padding, gaps, stack rhythm */
--space-1: 4px;
--space-2: 8px;
--space-3: 12px;
--space-4: 16px;
--space-5: 24px;
--space-6: 32px;
--space-7: 48px;
--space-8: 64px;
--space-9: 96px;
--space-10: 128px;

/* section rhythm tiers: vary band height for cadence, not one flat 180px */
--rhythm-tight: 96px;        /* dense utility bands (proof, footnote) */
--rhythm-base: 140px;        /* default band */
--rhythm-loose: 200px;       /* hero, closing CTA, the breathing moments */
/* note: existing --section-rhythm (180px) stays for back-compat */

/* type scale (px), paired with the families already declared */
--text-xs: 12px;             /* eyebrow, citation, legal */
--text-sm: 13px;             /* labels, captions, mono ticks */
--text-base: 16px;           /* body, the reading default */
--text-md: 18px;             /* lede, intro prose */
--text-lg: 21px;             /* large pull statement */
--text-h3: 22px;             /* sub-section heading (Inter 600) */
--text-h2: clamp(28px, 3.2vw, 34px);  /* section heading (Inter 600) */
/* H1 stays locked in global.css: Fraunces 600 clamp(44px, 5.5vw, 60px) */

/* line-heights */
--lh-tight: 1.05;            /* H1 (already set) */
--lh-heading: 1.2;           /* H2 to H4 */
--lh-snug: 1.4;              /* lede, large statements */
--lh-body: 1.6;              /* paragraphs */

/* radii: one step smaller than the card radius for inner elements */
--radius-sm: 8px;            /* chips, inputs, inner blocks */
--radius-card: 14px;         /* matches existing --radius */
--radius-pill: 999px;        /* tier pills, region switch */

/* borders: the existing --border (#e4e2dc) and --border-strong (#d4d1c9)
   plus one accent-weighted edge for the active / selected state */
--border-accent: #0d3b2e;

/* elevation: almost flat. One restrained shadow for hover-raise only. */
--shadow-raise: 0 1px 2px rgba(13, 17, 23, 0.04),
                0 8px 24px rgba(13, 17, 23, 0.06);

/* motion tokens (see section 5) */
--ease-out: cubic-bezier(0.2, 0.8, 0.2, 1);
--ease-in: cubic-bezier(0.4, 0, 1, 1);
--dur-fast: 120ms;           /* link / icon colour */
--dur-base: 180ms;           /* hover state, border, transform */
--dur-reveal: 420ms;         /* on-scroll section reveal */
--reveal-shift: 14px;        /* translateY distance for reveals */
```

If you mirror tokens into the `@theme` block for Tailwind utility generation, add the matching `--color-*`, `--spacing-*`, and `--radius-*` entries there too. The runtime `:root` block above is the source of truth for inline SVG and hand-written CSS.

### 1.2 Type system (roles)

Three families, strict role separation. This is already correct in the repo; documented here so builders do not drift.

| Role | Family | Weight | Size token | Line-height | Notes |
| --- | --- | --- | --- | --- | --- |
| H1 (display) | Fraunces | 600 (only self-hosted face) | locked clamp 44 to 60 | `--lh-tight` | Hero only. One per page. `letter-spacing: -0.01em`. |
| H2 (section) | Inter | 600 | `--text-h2` | `--lh-heading` | Not Fraunces. Keeps Fraunces scarce and special. |
| H3 (sub) | Inter | 600 | `--text-h3` | `--lh-heading` | Card titles, sub-sections. |
| Lede / intro | Inter | 400 | `--text-md` | `--lh-snug` | Muted (`--text-muted`). Max measure 60ch. |
| Body | Inter | 400 | `--text-base` | `--lh-body` | Ink. Measure 65 to 75ch. |
| Label / eyebrow | Inter | 600 | `--text-xs` | 1.2 | Uppercase, `letter-spacing: 0.08em to 0.1em`, `--text-faint`. |
| Numerals / code | Geist Mono | 400 to 500 | inherits | inherits | Every number, tick, price, year. `tabular-nums` forced globally. |
| Pull statement | Fraunces | 600 | `--text-lg` to 28px | `--lh-snug` | Optional, one per page max, for a single editorial quote. |

Discipline note on Fraunces. Only the 600 latin face is self-hosted. Use Fraunces for the H1 and at most one pull statement per page. Everything else, including all H2 to H6, is Inter. This scarcity is what gives the serif its weight; spread it across every heading and it becomes generic.

### 1.3 Elevation philosophy

Mostly flat. Hierarchy is hairlines plus whitespace. Three allowed depth treatments, in order of preference:

1. Hairline (`1px solid var(--border)`): default separation for cards, bands, table rows.
2. Surface shift: `--bg-card` (white) on `--bg` (off-white), or `--bg-raised` for alternating bands. A 4 to 8 percent value difference, no border needed when the contrast carries it.
3. Hover-raise shadow (`--shadow-raise`): the single shadow in the system. Applied only to interactive cards on hover, paired with a 1px upward translate. Never on static elements.

Never stack two depth treatments on the same element at rest (no border plus shadow plus surface shift). One at a time.

---

## 2. Hero visual

Asset shipped: `src/assets/motifs/hero-boundary.svg`.

### 2.1 Concept

Data held inside a boundary. A single, deliberately irregular perimeter (a building footprint, not a circle) drawn on a faint hex-lattice field. Inside the perimeter sits a small, orderly network of data nodes connected by lines, with one node in the forest accent (the contained system's core). One dashed line reaches toward the wall and stops at it with no node beyond, a quiet statement that nothing crosses. Corner registration ticks give it a drafted, institutional feel.

Why this and not a gradient blob: the cliche is soft, glowing, and centred. This is hard-edged, off-centre, and drawn like a plan drawing. It reads as architecture and law, not as generic tech.

### 2.2 Theming and integration

The motif inherits colour from the host page via `currentColor` and a small set of CSS classes, so it re-tints if the palette ever moves and never fights the page. It carries safe inline defaults (ink line work, white nodes, one forest node) so it renders correctly even with no host CSS.

Recommended host CSS when the builder wires it in (not wired here, per scope):

```css
.motif--boundary { color: var(--text); }                 /* ink line work */
.motif--boundary .m-faint   { stroke: var(--border-strong); }
.motif--boundary .m-accent  { stroke: var(--accent); }
.motif--boundary .m-accent-fill { fill: var(--accent); }
.motif--boundary .m-node    { fill: var(--bg-card); }
```

Placement guidance (the hero markup itself is locked per SPEC, so this is a layout note for the builder, not an instruction to change copy):

- The hero is a prose-led, left-aligned column (existing `.shell .narrow`, 800px). The motif belongs to the right of the column on desktop (a two-column hero: text 1.1fr, motif 0.9fr) and below the lede on mobile, scaled down and reduced in opacity to ~0.5 so it never competes with the headline.
- Keep the motif large and bled slightly off the right edge of the viewport for an editorial, grid-breaking feel. Do not box it in a card.
- viewBox is `0 0 720 540`; it scales cleanly. Target a rendered width of 440 to 560px on desktop.

### 2.3 Social / OG fallback

Asset shipped: `src/assets/motifs/og-cover.svg` (1200 x 630, fully self-contained, all colours baked).

Build-time note for the owner: most social platforms do not reliably render SVG Open Graph images, and several rasterize them. Rasterize `og-cover.svg` to a PNG at 1200 x 630 during the build (resvg-js or sharp) and reference the PNG in the `og:image` meta tag. Keep the SVG as the editable source. The render environment needs Fraunces 600 and Inter installed, or the baked-in fallbacks (Georgia, Helvetica) will be used. This is the one build-time choice that needs an owner decision: add a rasterize step, or ship a hand-exported PNG once and commit it.

---

## 3. Diagrams

Three diagrams, all shipped as clean, labelled, on-brand inline SVG. None are wireframe boxes; each carries a verdict and a caption built in. All three inherit colour from the host via a shared `.diagram` class set, with safe inline defaults.

Shared host CSS (wire once, applies to all three):

```css
.diagram { color: var(--text); }
.diagram .d-faint   { stroke: var(--border-strong); }
.diagram .d-axis    { stroke: var(--border-strong); }
.diagram .d-muted   { fill: var(--text-muted); }
.diagram .d-grey    { stroke: var(--grey); fill: var(--grey); }
.diagram .d-accent  { stroke: var(--accent); fill: var(--accent); }
.diagram .d-accent-line { stroke: var(--accent); }
.diagram .d-accent-text { fill: var(--accent); }
.diagram .d-accent-ring { stroke: var(--accent); }
.diagram .d-accent-dash { stroke: var(--accent); }
.diagram .d-surface { fill: var(--bg-card); }
.diagram .d-risk        { stroke: var(--risk); fill: var(--risk); }
.diagram .d-risk-text   { fill: var(--risk); }
.diagram text { font-family: var(--font-sans); }
.diagram .d-num { font-family: var(--font-mono); font-variant-numeric: tabular-nums; }
```

### 3.1 Tier control spectrum

Asset: `src/assets/diagrams/tier-spectrum.svg` (viewBox `0 0 760 360`).

Own, Colocate, Shared Cloud arranged left to right as one continuum, not three unrelated boxes. Isolation is encoded by the weight of the enclosing wall (thick solid for Own, medium for Colocate, thin dashed for Shared Cloud) and by the depth of nested rings (Own has two insets, Colocate one, Shared Cloud none). A baseline rail with "MOST ISOLATION" to "LEAST ISOLATION" labels runs across the top; chevrons connect the tiers. A wedge beneath, labelled "OPERATIONAL BURDEN YOU CARRY", thins left to right (High to Low), so the page can say plainly that more control costs more operational work. Each tier carries its own one-line descriptor (on-prem your building / dedicated box in-region DC / region-locked auditable).

Use on: Home (the delivery-tiers band) and the Pricing or Use Cases page where the spectrum is explained. Render width 640 to 760px.

### 3.2 Data boundary diagram (the good version)

Asset: `src/assets/diagrams/data-boundary.svg` (viewBox `0 0 760 380`).

One shared source (the prompt) splits into two paths. Left: Hive on-prem, where model and data both sit inside a solid forest-ticked boundary, connected by a closed loop (it cycles, it never exits), with a forest check verdict "Data never moves" and the line "Out of reach of foreign legal process." Right: hosted inference, where a red arrow crosses out of a dashed (broken) boundary into a "US-REACHABLE CLOUD", the hosted model and three grey retained-copy blocks sit inside, and a red marker reads "Residency risk" over the caption "Retained, logged, discoverable."

This is the one diagram where red appears, and it appears only on the act of leaving the boundary and the risk verdict, exactly per SPEC. Cloud chrome is grey, not red.

Use on: Home (the exposure band) and the Security and Data Residency page as the centrepiece. Render width 680 to 760px. Pair it with the existing `JurisdictionDiagram` component or replace that component's art with this asset.

### 3.3 Cost crossover curve (conceptual, static)

Asset: `src/assets/diagrams/cost-crossover.svg` (viewBox `0 0 720 420`).

Cumulative cost (y) versus time and usage (x), no numbers. Metered cloud is a steady steep grey line that never stops climbing (with an italic "the meter never stops" at the axis end). Owned or colocated hardware is a forest line: an upfront vertical step (capex, labelled "Upfront, the box you own") then a near-flat slope. The two cross at a marked, ringed "Breakeven" point with the qualitative caption "after this, owning costs less" and a faint dropline. Direct inline labels on each line, no detached legend. Faint gridlines carry no values.

This is the STATIC companion to the real Recharts TCO island on Pricing. The Recharts island stays the only JS chart and carries the actual modelled 36-month numbers. This SVG states the shape of the argument anywhere else on the site (Home cost-wedge band, Use Cases) without loading the island. Colour rule observed: owned forest, cloud grey, red not used. Render width 600 to 720px.

Build-time note: the crossover point coordinate (x=292, y=253) is computed from the two line equations documented in the file's comment. If a builder edits either line's path, recompute the marker so it stays on the intersection.

---

## 4. Imagery and texture plan

Default to SVG and CSS, not stock photography. Stock photos of server rooms, handshakes, or glowing locks are the exact AI-slop cliche to avoid, and they undercut the institutional tone. Where the page needs visual interest, it comes from drafted line work and composed whitespace.

### 4.1 Per-page treatment

| Page | Visual treatment | Asset / method |
| --- | --- | --- |
| Home | Hero boundary motif (right of headline). Hex lattice at ~5% behind the hero and behind each band's eyebrow row. Data-boundary diagram in the exposure band. Tier-spectrum in the delivery band. Cost-crossover in the cost-wedge band. | `hero-boundary.svg`, `hex-lattice.svg`, `data-boundary.svg`, `tier-spectrum.svg`, `cost-crossover.svg` |
| Security and Data Residency | Data-boundary diagram as the centrepiece. Hex lattice forest-tinted at ~4% behind the page header. A jurisdiction note can reuse the existing `JurisdictionDiagram`. | `data-boundary.svg`, `hex-lattice.svg` (`.is-forest`) |
| Pricing | Real Recharts TCO island (the one JS island). Cost-crossover SVG can appear above the fold as the conceptual primer before the numeric chart. Hardware capex as the existing `StaticBar`. | `cost-crossover.svg`, existing chart components |
| Use Cases | Per-sector cards (law, finance, healthcare, SMB). Each card carries a small monochrome line glyph drawn in the same hairline style as the diagrams (a scales-of-justice abstraction, a ledger, a cross, a building). Do not use emoji or stock icons. | New small SVG glyphs in the diagram style (build as needed, same `.diagram` class set) |
| All pages | Hex lattice as the universal quiet substrate. Footer sits on `--bg-raised` with a hairline top. | `hex-lattice.svg` |

### 4.2 Hex lattice usage

Asset: `src/assets/motifs/hex-lattice.svg` (240 x 208, tiles seamlessly).

Use as a low-opacity CSS background, not inline, when it is pure texture:

```css
.lattice-field {
  background-image: url('/src/assets/motifs/hex-lattice.svg'); /* or import via Astro */
  background-repeat: repeat;
  opacity: 0.05;            /* ink lattice on paper */
}
```

For an inline, re-tintable version (so it follows the palette and respects dark headers), drop the SVG inline and set `.motif--lattice { color: var(--text); opacity: 0.05; }` or `.is-forest { color: var(--accent); opacity: 0.04; }`. Keep opacity in the 4 to 7 percent range. Above that it stops being texture and becomes noise.

### 4.3 If raster imagery is ever required

Only one case justifies a real photograph: a credibility shot of actual delivered hardware (the box the customer owns) on the Pricing or Use Cases page, if and when one exists. It must be a real photo of the real product, shot plain on a neutral background, not stock. If that is not available, do not substitute stock; use the cost-crossover and capex-bar visuals instead.

If a generated hero background is ever wanted as an alternative to the line motif (not recommended, the line motif is stronger), the prompt would be: "Top-down architectural plan drawing of a single building footprint, fine ink line work on warm off-white paper, faint hexagonal survey grid, one element picked out in deep forest green, drafting-table aesthetic, no text, no people, no glow, no gradient." Placement: full-bleed behind the hero at 8 to 12 percent opacity. Again, prefer the shipped SVG; this is a fallback only.

---

## 5. Motion

CSS-first, CSP-safe (no inline script), accessible. All motion respects `prefers-reduced-motion`, which is already globally honoured in `global.css` (animations and transitions collapse to ~0ms). The tokens in section 1.1 (`--ease-out`, `--dur-*`, `--reveal-shift`) drive everything below.

### 5.1 On-scroll section reveal (the signature moment)

One well-orchestrated reveal per section beats scattered micro-animations. Use the CSS-only `animation-timeline: view()` pattern where supported, with a static fallback (content visible by default, animation is pure enhancement). No JavaScript, no IntersectionObserver, CSP-clean.

```css
@media (prefers-reduced-motion: no-preference) {
  @supports (animation-timeline: view()) {
    .reveal {
      animation: reveal-in linear both;
      animation-timeline: view();
      animation-range: entry 0% entry 40%;
    }
    /* stagger children: each child a small delay via :nth-child */
    .reveal > * { animation: reveal-in linear both; animation-timeline: view();
                  animation-range: entry 5% entry 45%; }
    .reveal > *:nth-child(2) { animation-range: entry 10% entry 50%; }
    .reveal > *:nth-child(3) { animation-range: entry 15% entry 55%; }
  }
}
@keyframes reveal-in {
  from { opacity: 0; transform: translateY(var(--reveal-shift)); }
  to   { opacity: 1; transform: translateY(0); }
}
```

Animate only `opacity` and `transform` (GPU-friendly, no layout reflow, no CLS). Default state must be fully visible so browsers without `animation-timeline` and reduced-motion users see complete content.

### 5.2 Hover states

```css
/* interactive card: hairline to accent edge, plus the one allowed shadow */
.card-raise {
  transition: border-color var(--dur-base) var(--ease-out),
              box-shadow var(--dur-base) var(--ease-out),
              transform var(--dur-base) var(--ease-out);
}
.card-raise:hover {
  border-color: var(--border-accent);
  box-shadow: var(--shadow-raise);
  transform: translateY(-1px);
}

/* link: forest underline grows from left, never a colour flash */
.textlink {
  background-image: linear-gradient(var(--accent), var(--accent));
  background-size: 0% 1px;
  background-position: 0 100%;
  background-repeat: no-repeat;
  transition: background-size var(--dur-base) var(--ease-out);
  color: var(--accent);
}
.textlink:hover { background-size: 100% 1px; }
```

### 5.3 Hero motif entrance (optional, restrained)

A single draw-in of the boundary path on load, gated behind reduced-motion. Use `stroke-dasharray` / `stroke-dashoffset` on the `.m-boundary` path only (the wall draws itself once), 600 to 800ms, `--ease-out`, then static. Nodes fade in with a short stagger. Do not loop, do not animate on scroll. If this adds complexity, skip it; the static motif is already strong.

### 5.4 What not to animate

No parallax. No auto-playing loops. No animated gradients. No motion on body text. No bounce or overshoot easing (this is institutional, not playful). No animation that shifts layout. Exit animations, where any exist, run at ~70% of enter duration.

---

## 6. Component and layout specs

The goal is varied rhythm across pages within one system, so nothing feels like the same template stamped four times. Below: section templates, the tier block, pricing layout, use-case cards, nav, footer.

### 6.1 Section band system (rhythm variation)

The repo already has a `.band` / `.band--raised` system. Extend it with rhythm tiers so vertical cadence varies:

- `.band` default: `padding-block: var(--rhythm-base)` (140px), on `--bg`.
- `.band--raised`: on `--bg-raised`, hairline top and bottom border, for alternating structure.
- `.band--loose`: `var(--rhythm-loose)` (200px), for the hero and the closing CTA, the breathing moments.
- `.band--tight`: `var(--rhythm-tight)` (96px), for utility content (proof callout, footnote).
- `.band--forest`: background `--bg-forest-tint`, a barely-there wash, used once per page maximum to mark the single most important band (usually the data-boundary or the tier spectrum).

Alternate `--bg` and `--bg-raised` down the page so structure reads without colour. Vary which bands are loose vs tight per page so Home, Security, Pricing, and Use Cases do not share an identical silhouette.

Inside each band, two measure widths: `.narrow` (800px, prose-led) and the full `.shell` (1140px, for diagrams and grids). Mixing the two within a page is what creates editorial rhythm. Lead a band with a narrow prose intro, then break to a full-width diagram.

Eyebrow plus heading pattern (the section opener):

```html
<p class="eyebrow">Data residency</p>     <!-- xs, uppercase, faint, 0.1em -->
<h2 class="section-h2">...</h2>            <!-- Inter 600, --text-h2 -->
```

Optionally precede the eyebrow with a 24px hairline rule or a small forest tick to signal section start, varied per page.

### 6.2 Tier comparison block

Built around `tier-spectrum.svg`. Two presentations, pick per page:

1. Spectrum-led (Home): the full-width `tier-spectrum.svg` as the hero of the band, with three short descriptor columns beneath it aligned to the three tiers. No table.
2. Comparison detail (Pricing or a dedicated page): a three-column card row. Each column is a `.card` (white, hairline, `--radius-card`, `--space-5` padding) with: a pill (`--radius-pill`, `--accent-wash` background, forest text) naming the tier, a one-line claim, a short feature list (Inter 400, `--space-3` row gap), and an isolation / ops indicator drawn as three small bars matching the spectrum encoding. The middle tier (Colocate) gets a `--border-accent` edge to mark it as the common recommendation. One primary CTA across the row, not three.

Do not build a dense feature-matrix table; it reads as a SaaS pricing grid and breaks the institutional tone. Keep it to claims and a single qualitative isolation indicator.

### 6.3 Pricing layout

Order (per SPEC sections 9, 10, 12): hero (no exact dollar figure in the heading), on-prem ladder (Strix entry, DGX step-up, annual licence, hardware capex `StaticBar`), the conceptual cost-crossover SVG as a primer, the real Recharts TCO island (`client:visible`, the only JS island, the only route with the scoped `style-src-attr` relaxation), the Hive Cloud tier (regional, a fraction of US pricing, no sovereignty or residency language), the Pinterest cost proof with its disclaimer, the verbatim pricing disclaimer, the closing CTA.

Layout craft: numbers everywhere in Geist Mono with tabular figures (already enforced). The capex bar and the crossover sit in `ChartFrame` (headline, plain-reading line, required citation footer). Keep the Cloud tier visually quieter than the on-prem ladder (it is the lowest tier in the control spectrum); a single card on `--bg-raised`, not a bright panel.

### 6.4 Use-case cards

A responsive grid (2 columns desktop, 1 mobile) of sector cards: law, finance, healthcare, privacy-sensitive SMB. Each card:

- White surface, `--radius-card`, `1px solid var(--border)`, `--space-6` padding.
- A small monochrome line glyph top-left, drawn in the diagram hairline style (not emoji, not a filled icon set). Ink stroke, ~28px, one optional forest detail.
- H3 (Inter 600) sector name, a two-line plain claim (Inter 400, `--text-muted`), and a single `.textlink` ("How law firms use Hive").
- Hover: `.card-raise` (accent edge, the one shadow, 1px lift).

Vary the grid: make one card span two columns (a featured sector, usually law for the Canadian legal-discovery wedge) so the grid is asymmetric, not a tidy 2x2.

### 6.5 Nav

The existing nav is sound. Refinements:

- Brand mark: the existing 14px forest square is fine; optionally swap it for a 14px inline version of the boundary motif's accent node (a small forest-ringed dot) for a subtle tie to the hero. Keep it tiny.
- Sticky, `backdrop-filter: blur(8px)` on a `color-mix` of `--bg` at 88% (already present). Add a hairline bottom border only after scroll if a scroll class is wired; otherwise the static hairline is fine.
- Links: muted to ink on hover and active, via `--dur-fast`. Active page carries the ink colour and an optional 2px forest underbar.
- CTA ("Book a Demo"): solid forest (`--accent`) with `--bg` text, `--radius-sm` to `10px`. The single filled element in the nav. On hover, `filter: brightness(1.05)` (already present) or a `--dur-base` shift to `--accent-hover`.

### 6.6 Footer

The existing three-column footer (brand / nav / region) on `--bg-raised` with a hairline top is correct. Refinements:

- Add a faint hex-lattice strip behind the footer brand block at ~4% for quiet texture continuity.
- Keep the entity name (S Cubed Technology Ltd.) and tagline ("Your AI runs on your hardware. Your data never moves.") in the brand column. Tagline in `--text-muted`.
- Region switcher anchors (`?geo=co` Global, `?geo=bd` Bangladesh) stay; style as small `.textlink` items.
- Legal row: year in Geist Mono (already `data-numeric`), `--text-faint`, `--text-xs`.

### 6.7 Reusable pieces summary

For the builders: the asset files in `src/assets/` are drop-in. The token additions in section 1.1 are paste-ready. The CSS snippets in sections 3, 5, and 6 are reference implementations, not yet wired (per scope). No `.astro` page is changed by this document.

---

## 7. Page-by-page silhouette (so pages differ)

To prevent same-shape monotony, each page gets a distinct vertical silhouette:

- Home: loose hero (motif right) -> raised exposure band (data-boundary diagram, forest wash) -> base delivery band (tier spectrum) -> base cost band (crossover SVG) -> tight proof band -> loose closing CTA. Alternating, with two full-width diagram breaks.
- Security and Data Residency: loose header (lattice forest-tint) -> narrow prose exposure -> full-width data-boundary centrepiece (forest band) -> jurisdiction detail -> base FAQ-style stack -> closing CTA. More prose, one big diagram.
- Pricing: tight hero (no dollar figure) -> on-prem ladder (capex bar) -> crossover primer -> Recharts TCO island -> quieter Cloud card -> tight proof -> verbatim disclaimer -> closing CTA. Number-dense, two charts, quietest Cloud tier.
- Use Cases: base header -> asymmetric sector card grid (one 2-col span) -> a single tier-spectrum recap -> closing CTA. Card-led, lightest on prose.

Same system, four different rhythms. That is the difference between designed and templated.

---

## 8. Asset inventory

All files are CSS-themeable (inherit via `currentColor` and class hooks), carry safe inline defaults, have a `viewBox`, contain no `<script>` and no external references, and are CSP-safe.

| File | viewBox | Purpose |
| --- | --- | --- |
| `src/assets/motifs/hero-boundary.svg` | 0 0 720 540 | Hero motif: data held inside a boundary. |
| `src/assets/motifs/hex-lattice.svg` | 0 0 240 208 | Seamless tiling texture substrate. |
| `src/assets/motifs/og-cover.svg` | 0 0 1200 630 | Social / OG cover, baked colours; rasterize to PNG at build. |
| `src/assets/diagrams/tier-spectrum.svg` | 0 0 760 360 | Own / Colocate / Shared Cloud control spectrum. |
| `src/assets/diagrams/data-boundary.svg` | 0 0 760 380 | Data stays in vs leaves to US-reachable cloud (red = residency risk). |
| `src/assets/diagrams/cost-crossover.svg` | 0 0 720 420 | Conceptual cumulative-cost crossover, static, no numbers. |

---

## 9. Owner decisions needed

1. OG image build step: add a build-time rasterizer (resvg-js / sharp) to turn `og-cover.svg` into a 1200 x 630 PNG, or hand-export a PNG once and commit it. SVG OG images are unreliable on social platforms. This is the only blocking build-time choice.
2. Hero motif entrance animation (section 5.3): ship the one-time draw-in, or keep the motif fully static. Static is the safe, recommended default.
3. Use-case sector glyphs (section 6.4): commission four small line glyphs in the diagram style now, or defer until the Use Cases page is built. They are not yet produced (out of the named deliverable scope).
```

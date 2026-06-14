# Sovereign Home QA punch-list (owner markings, 2026-06-14)

Status: documented for execution. Site builds green at commit 70f93bb. Work paused because the org monthly Anthropic spend cap was hit (fix agent returned the spend-limit error). Raise the limit at claude.ai/admin-settings/usage, then execute the items below (best done with the screenshot eval loop).

All file paths under `website/sovereign/`. Keep: dossier system, ink hero, palette (forest/ink/off-white, no teal/gold), AA contrast, CSP-safe (no inline style attrs, no inline script), zero client JS on home, claim rules (no "compliant/certified", court line verbatim, "~10x to ~30x" not "150x", no named Hive customer, Canadian English, no dash punctuation).

## 1. Hero image cut / pixelated / not responsive
File: `src/pages/index.astro` `.hero__media` + `.hero__grid` + `.hero__img`.
Diagnosis: `.hero__grid` defines a single column (`minmax(0,52%)`) while `.hero__media` is `position:absolute; width:52vw` overlaying the right. Between ~881px and ~1100px the absolute 52vw panel can overlap the 52% text column. `object-fit:cover; object-position:65% center` crops the appliance at wide ratios; emitted widths cap at 1392 so very wide screens upscale (pixelation).
Fix: drive the hero as a real 2-col grid (text col + media col) instead of an absolute overlay, OR clamp the media panel and add a min gap so it never overlaps text. Add larger `widths` (e.g. 1600, 1920) + raise quality; tune `object-position` so the appliance never crops out; verify at 1920/1440/1280/1024/900/768/375 with no overlap, no upscaling blur, no cut.

## 2 + 3 + 9. Missing source URLs on news/proof claims
Files: `src/components/TrustStrip.astro` (Perplexity + Pinterest stats), `src/pages/index.astro` sec 04 Pinterest CalloutCard + sec 08 Perplexity proof + the court-order line (sec 02 exposure 02).
Fix: add clickable citation links to a real source for each factual/news claim. Keep the court sentence verbatim and generic (no case/provider/date inline); the link may point to a neutral source. Need the actual source URLs (pull from SPEC.md if present, else research). Flag any with no source rather than inventing one.

## 4 + 5 + 8 + 12. Unbalanced text / boxes (non-uniform)
Files: `src/pages/index.astro` `.cards`, `.exposures`, `.proofs` grids + their cards.
Diagnosis: 1fr grid columns are equal width but ragged content makes heights/whitespace look uneven; headings wrap unevenly.
Fix: enforce visual uniformity — equal internal padding, consistent measure, `text-wrap: balance` on card/section headings and `text-wrap: pretty` on bodies, align card footers, even out copy lengths so the three/four columns read as a set. This is a visual-tuning pass (use the screenshot loop).

## 6. Data-boundary figure (Fig. 1)
File: `src/assets/diagrams/data-boundary.svg`.
Fix: (a) the unlabeled grey boxes look like missing content — label them ("retained copy", "log", "backup") or remove; (b) the red "Residency risk" mark overlaps the "Third-party cloud" header — reposition as a clean callout clear of the header; (c) make the caption match what the figure shows; (d) fix left/right alignment so the two sides read as a balanced pair. Red stays only on the egress.

## 7. Tier-spectrum figure (Fig. 2) — looks weird, out of place
File: `src/assets/diagrams/tier-spectrum.svg` + its Figure usage in sec 03.
Fix: redesign into a cleaner, more elegant treatment of Own -> Colocate -> Shared (decreasing isolation, decreasing operational burden). A refined horizontal comparison reads better than the current diagram. Keep semantics correct.

## 10. Audience cards -> case studies (sec 05)
Files: `src/pages/index.astro` sec 05 cards (currently all `href="/use-cases"`), and `src/pages/use-cases.astro` (add anchor ids).
Fix: add anchor ids on use-cases.astro (`finance`, `healthcare`, `government`, `legal`, `sensitive`) and point each audience card to `/use-cases#<id>`. Same for clarity on the tier cards if each tier has a section. Make the 5 cards uniform.

## 11. Section 06 reads empty
File: `src/pages/index.astro` sec 06 + `.improve__img`.
Diagnosis: heading + one paragraph, then a large whitespace gap and the abstract `slot6-1` hex image reads as filler. Section is ~1091px tall with ~627 chars.
Fix: tighten the section (remove the dead band), and either make the image a genuinely relevant figure or replace it with a cleaner layout (e.g. a small "single box -> multi-GPU -> multi-node" capacity ladder graphic that matches the copy). No empty placeholder feel.

## 13. Footer feels empty
File: `src/components/Footer.astro`.
Diagnosis: footer renders brand + 5-link list + region switcher + copyright, but it is sparse and the large `margin-top: var(--section-rhythm)` plus the closing CTA leaves a big empty band before it.
Fix: enrich into grouped columns (Product, Company, Region), add the logo mark + a one-line positioning tagline + contact, and reduce the dead gap above it so it reads finished.

---
Once the spend limit is raised, run these with the eval loop: fix -> `ALLOW_UNVERIFIED=1 npm run build` -> screenshot each touched area -> verify against this list -> commit.

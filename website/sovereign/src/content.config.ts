// Content collections for the Hive sovereign site.
//
// Two collections:
//   pages  — MDX page content with template, hero, vertical, scope, and the
//            source citations that back every factual claim.
//   charts — JSON chart data. Every chart MUST carry a source. A chart with a
//            missing source fails the build (zod .refine). A chart marked
//            verified:false fails the PRODUCTION build unless ALLOW_UNVERIFIED
//            is set, so unverified figures can never ship by accident.
//
// Astro 6 places this file at src/content.config.ts and uses glob()/file()
// loaders. zod is imported from astro/zod.
import { defineCollection, z } from 'astro:content';
import { glob, file } from 'astro/loaders';

// In production (astro build), unverified chart data is forbidden unless the
// operator explicitly opts in. Dev (astro dev) always allows it so authors can
// work on charts before figures are re-pulled and verified.
const IS_PRODUCTION_BUILD = import.meta.env.PROD === true;
const ALLOW_UNVERIFIED = import.meta.env.ALLOW_UNVERIFIED === '1';

const heroSchema = z.object({
  headline: z.string(),
  // Teal benefit ticks shown beneath the H1.
  subTicks: z.array(z.string()),
  // Faint scope clarifier line under the ticks (no tick mark).
  scopeClarifier: z.string().optional(),
  ctaLabel: z.string(),
  ctaHref: z.string(),
  secondaryLabel: z.string().optional(),
  secondaryHref: z.string().optional(),
});

const sourceSchema = z.object({
  claim_id: z.string(),
  url: z.string().url(),
  label: z.string(),
  retrieved: z.string(),
});

const pages = defineCollection({
  loader: glob({ pattern: '**/[^_]*.{md,mdx}', base: './src/content/pages' }),
  schema: z.object({
    title: z.string(),
    description: z.string(),
    template: z.enum(['home', 'security', 'pricing', 'usecase']),
    order: z.number(),
    eyebrow: z.string().optional(),
    hero: heroSchema.optional(),
    // Ids of charts (charts collection) rendered on this page.
    charts: z.array(z.string()).optional(),
    vertical: z.enum(['legal', 'finance', 'healthcare', 'smb']).optional(),
    // Deployment scope this content describes. Drives the sovereignty-wording
    // MDX lint (sovereignty language is only legitimate in onprem scope).
    scope: z.enum(['onprem', 'cloud', 'both']),
    // Citations backing every <Claim id="..."> used in the body.
    sources: z.array(sourceSchema).optional(),
    draft: z.boolean().default(false),
    updated: z.coerce.date(),
  }),
});

const chartSourceSchema = z.object({
  citation: z.string(),
  publisher: z.string(),
  accessed: z.string(),
  provenance: z.string(),
});

const charts = defineCollection({
  loader: file('./src/data/charts.json'),
  schema: z
    .object({
      id: z.string(),
      title: z.string(),
      unit: z.string(),
      ratesAsOf: z.string(),
      assumptions: z.string().optional(),
      // A chart with no source is invalid. This object is required; zod fails
      // the build if it is missing or malformed.
      source: chartSourceSchema,
      verified: z.boolean(),
      // Series shape is chart-specific; validated structurally as labelled
      // numeric points. Static SVG charts and the Recharts island both read
      // this shape.
      series: z.array(
        z.object({
          name: z.string(),
          color: z.enum(['teal', 'grey', 'red', 'gold']).optional(),
          points: z.array(
            z.object({
              label: z.string(),
              value: z.number(),
            }),
          ),
        }),
      ),
    })
    // Citation gate: source is already required above, but assert provenance is
    // non-empty so a blank citation cannot slip through. A chart with an empty
    // citation is treated as having no source and fails the build.
    .refine((c) => c.source.citation.trim().length > 0, {
      message:
        'Chart source.citation is empty. Every chart must carry a real citation; never ship a fabricated or blank source.',
      path: ['source', 'citation'],
    })
    // Verification gate: unverified data must not ship to production unless the
    // operator sets ALLOW_UNVERIFIED=1. Dev builds always allow it.
    .refine((c) => c.verified || !IS_PRODUCTION_BUILD || ALLOW_UNVERIFIED, {
      message:
        'Chart is verified:false and this is a production build. Re-pull and verify the figure, or set ALLOW_UNVERIFIED=1 to override (pre-publish only).',
      path: ['verified'],
    }),
});

export const collections = { pages, charts };

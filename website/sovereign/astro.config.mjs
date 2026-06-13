// @ts-check
import { defineConfig } from 'astro/config';
import mdx from '@astrojs/mdx';
import react from '@astrojs/react';
import contentLint from './src/integrations/content-lint.ts';

// Site: hive.scubed.co (rest of world). The Bangladesh site lives separately
// at hive.scubed.com.bd and is not part of this project.
//
// Tailwind 4 is wired through @tailwindcss/postcss (see postcss.config.mjs).
// The legacy @astrojs/tailwind integration is deprecated in Astro 6 and is not
// used. The @tailwindcss/vite plugin is also avoided here: it bundles its own
// vite 8 / rolldown and binds against APIs Astro's vite 7 does not expose
// (Missing field `tsconfigPaths`), which breaks the production build. The
// PostCSS plugin runs inside Astro's own vite pipeline and sidesteps that.
//
// Content Security Policy is carried entirely by public/_headers (the
// transport-layer policy Cloudflare Pages applies per route). Astro's built-in
// security.csp is intentionally NOT used here.
//
// Why: with output: 'static', Astro's CSP writes a per-page
// <meta http-equiv="content-security-policy"> that lists style-src 'self' plus
// hashes but no style-src-attr. Browsers enforce the intersection of the meta
// CSP and the _headers HTTP CSP, and a missing style-src-attr falls back to
// style-src. That intersection is stricter than the _headers style-src-attr
// 'unsafe-inline' relaxation scoped to /pricing, so the meta tag would block
// the inline style attributes Recharts writes on its SVG nodes and break the
// TCO chart in production. One coherent CSP avoids the conflict: _headers
// already encodes the strict baseline for every route plus the single
// /pricing style-src-attr relaxation Recharts needs.
export default defineConfig({
  output: 'static',
  site: 'https://hive.scubed.co',
  integrations: [
    mdx(),
    react(),
    contentLint(),
  ],
  markdown: {
    // Shiki emits inline styles that are incompatible with a strict CSP. This
    // marketing site has no code blocks that need highlighting, so disable it
    // to keep style-src free of 'unsafe-inline'.
    syntaxHighlight: false,
  },
});

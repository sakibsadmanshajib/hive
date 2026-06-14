// @ts-check
import { defineConfig } from 'astro/config';
import mdx from '@astrojs/mdx';
import react from '@astrojs/react';
import sitemap from '@astrojs/sitemap';
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
// Content Security Policy is enforced by Astro's built-in, stabilized CSP. It
// hashes every framework-emitted inline script and style at build time, so the
// policy never needs 'unsafe-inline' for script-src or style-src. The route
// scoped relaxation that Recharts needs on the pricing page (inline style
// attributes only) is layered on top through the public/_headers file, which
// Cloudflare applies per route.
export default defineConfig({
  output: 'static',
  site: 'https://hive.scubed.co',
  integrations: [
    mdx(),
    react(),
    sitemap(),
    contentLint(),
  ],
  markdown: {
    // Shiki emits inline styles that are incompatible with a strict CSP. This
    // marketing site has no code blocks that need highlighting, so disable it
    // to keep style-src free of 'unsafe-inline'.
    syntaxHighlight: false,
  },
  security: {
    csp: {
      // No 'unsafe-inline'. Astro injects per-page hashes for any inline
      // script or style it emits.
      directives: [
        "default-src 'self'",
        "img-src 'self' data:",
        "font-src 'self'",
        "base-uri 'self'",
        "form-action 'self'",
        "frame-ancestors 'none'",
        "object-src 'none'",
      ],
      scriptDirective: {
        // 'self' plus the build-time hashes Astro adds automatically.
        resources: ["'self'"],
      },
      styleDirective: {
        resources: ["'self'"],
      },
    },
  },
});

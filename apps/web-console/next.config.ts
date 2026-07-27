import type { NextConfig } from "next";
import createNextIntlPlugin from "next-intl/plugin";

// CF Pages + Next 15 — keep config minimal; @cloudflare/next-on-pages
// handles edge runtime compat at build time.
const config: NextConfig = {
  images: {
    // CF Pages does not use Next's Node image optimizer
    unoptimized: true,
  },
  // Reduce build noise on CI
  productionBrowserSourceMaps: false,
};

// Points next-intl at ./i18n/request.ts. Routing-free mode, so no locale
// middleware is chained onto the existing Supabase/CSP middleware.
const withNextIntl = createNextIntlPlugin();

export default withNextIntl(config);

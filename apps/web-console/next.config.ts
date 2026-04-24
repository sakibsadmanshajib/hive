import type { NextConfig } from "next";

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

export default config;

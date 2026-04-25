import type { NextConfig } from "next";
import { initOpenNextCloudflareForDev } from "@opennextjs/cloudflare";

// Only spin up the Cloudflare bindings + miniflare runtime during
// `next dev`. Calling this during `next build` (production bundles in
// CI) starts a Workerd instance whose SQLite state directory races on
// some filesystems (WSL2, NTFS-mounted volumes), and the build doesn't
// need the dev bindings — the OpenNext bundler handles production
// bindings via wrangler.jsonc.
if (process.env.NODE_ENV === "development") {
  initOpenNextCloudflareForDev();
}

const config: NextConfig = {
  images: {
    unoptimized: true,
  },
  productionBrowserSourceMaps: false,
};

export default config;

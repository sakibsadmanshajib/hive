import type { NextConfig } from "next";
import { initOpenNextCloudflareForDev } from "@opennextjs/cloudflare";

initOpenNextCloudflareForDev();

const config: NextConfig = {
  images: {
    unoptimized: true,
  },
  productionBrowserSourceMaps: false,
};

export default config;

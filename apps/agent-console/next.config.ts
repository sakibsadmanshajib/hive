import type { NextConfig } from "next";

// Self-hosted sidecar (chat/enterprise Docker profiles only, no Cloudflare
// Workers deploy). Runs as a plain Node process, same Docker pattern as
// apps/web-console's Docker service (see deploy/docker/Dockerfile.agent-console).
//
// basePath matches the Caddy route this app is served under (see
// deploy/docker/Caddyfile.owui). Chosen to sit next to, not under, Open
// WebUI's own paths so Caddy can intercept it before OWUI's SPA catch-all
// ever sees the request -- see PR description for the collision analysis.
const config: NextConfig = {
  basePath: "/agent-workspace",
  productionBrowserSourceMaps: false,
};

export default config;

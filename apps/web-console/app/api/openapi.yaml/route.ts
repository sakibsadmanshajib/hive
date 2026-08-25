// Serves the generated OpenAPI contract so an integrator can point their own
// tooling at it instead of reading a rendered table.
//
// Deliberately outside /console: middleware.ts redirects unauthenticated
// requests under /console to sign-in, and a spec a code generator cannot fetch
// without a session cookie is not a spec anyone will use.
//
// The file is read from packages/openai-contract/ rather than bundled: it is
// 1.1 MB of YAML and no loader in this app turns that into a module. That
// makes the read a real runtime dependency on the image containing the
// package, which is why both deploy/docker/Dockerfile.web-console and
// deploy/docker/Dockerfile.web-console.prod COPY it in. A missing file answers
// 500 with a plain message rather than an empty 200, because an empty spec
// silently generates an empty client.
//
// Node runtime only. `npm run build:cf` (the retired Workers path) has no
// filesystem and this route would fail there.
import { readFile } from "node:fs/promises";

import { OPENAPI_SPEC_PATH } from "@/lib/api-contract";

export async function GET(): Promise<Response> {
  let spec: string;
  try {
    spec = await readFile(OPENAPI_SPEC_PATH, "utf8");
  } catch (error) {
    console.error("openapi spec unreadable", { path: OPENAPI_SPEC_PATH, error });
    return new Response(
      "The OpenAPI specification is not available in this deployment.\n",
      { status: 500, headers: { "content-type": "text/plain; charset=utf-8" } },
    );
  }

  return new Response(spec, {
    headers: {
      "content-type": "application/yaml; charset=utf-8",
      "content-disposition": 'inline; filename="hive-openapi.yaml"',
      "cache-control": "public, max-age=300",
    },
  });
}

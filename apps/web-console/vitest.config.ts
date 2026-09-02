import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";
import { resolve } from "path";

export default defineConfig({
  plugins: [react()],
  test: {
    environment: "jsdom",
    globals: true,
    // Several of these specs server-render a whole console page against a
    // mocked control-plane, which costs a second or two each on an idle
    // machine. Vitest's 5s default leaves no headroom for that under a loaded
    // suite, and the failure is ugly: a timed-out case never unmounts, so the
    // next case in the same file then fails with "found multiple elements"
    // and the report points at two innocent tests. Which specs tip over
    // changes from run to run, which is the signature of scheduler contention
    // rather than of any one spec. Nothing here asserts latency, so a real
    // hang still fails, just later.
    testTimeout: 20_000,
    setupFiles: [],
    include: ["**/__tests__/**/*.test.{ts,tsx}", "**/*.test.{ts,tsx}"],
    exclude: ["node_modules", ".next", "e2e"],
    coverage: {
      provider: "v8",
      include: ["app/**", "lib/**", "components/**"],
      exclude: ["lib/control-plane/permissions.generated.ts", "**/*.d.ts"],
    },
  },
  resolve: {
    alias: {
      "@": resolve(__dirname, "."),
    },
  },
});

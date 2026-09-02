import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";
import { resolve } from "path";

export default defineConfig({
  plugins: [react()],
  test: {
    environment: "jsdom",
    globals: true,
    // Several of these specs server-render a whole console page against a
    // mocked control-plane. The cost is cold transform and import time, and
    // it already exceeded vitest's 5s default on main before this setting
    // existed: the analytics overview spec was measured at 3127ms cold there
    // and 5133ms here, so the margin was gone either way. The failure is also
    // ugly beyond the one spec, because a timed-out case never unmounts and
    // the next case in the same file then fails with "found multiple
    // elements", pointing the report at an innocent test. Nothing here
    // asserts latency, so a genuine hang still fails, just later.
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

import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";
import { resolve } from "path";

export default defineConfig({
  plugins: [react()],
  test: {
    environment: "jsdom",
    globals: true,
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

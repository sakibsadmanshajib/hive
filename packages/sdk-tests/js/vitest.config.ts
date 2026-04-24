import { defineConfig } from "vitest/config";

export default defineConfig({
  test: {
    // Raised to 60s because openrouter/free routes across a heterogeneous
    // pool of free models where individual attempts + edge-api's bounded
    // 429/5xx retry can exceed the previous 30s budget.
    testTimeout: 60000,
    hookTimeout: 15000,
  },
});

import { defineConfig, devices } from "@playwright/test";

export default defineConfig({
  testDir: "./",
  timeout: 30_000,
  expect: { timeout: 10_000 },
  fullyParallel: false,
  retries: process.env.CI ? 1 : 0,
  reporter: [
    ["list"],
    ["html", { outputFolder: "../../../playwright-report-owui", open: "never" }],
  ],
  use: {
    baseURL: process.env.OWUI_URL ?? "http://localhost:3002",
    trace: "retain-on-failure",
    video: "retain-on-failure",
    screenshot: "only-on-failure",
  },
  projects: [
    { name: "owui-setup", testMatch: /owui\.setup\.ts$/ },
    {
      name: "owui",
      testMatch: /\d{2}-.*\.spec\.ts$/,
      dependencies: ["owui-setup"],
      use: { ...devices["Desktop Chrome"] },
    },
    {
      name: "owui-perf",
      testMatch: /performance\/.*\.spec\.ts$/,
      dependencies: ["owui-setup"],
      use: { ...devices["Desktop Chrome"] },
    },
  ],
});

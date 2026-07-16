import { defineConfig } from "vite";

// Standard Tauri + Vite wiring: fixed dev port the Rust side is told about
// via tauri.conf.json's devUrl, and the src-tauri build output ignored by
// Vite's watcher so a `cargo build` doesn't trigger a frontend reload loop.
const host = process.env.TAURI_DEV_HOST;

export default defineConfig({
  clearScreen: false,
  server: {
    port: 1420,
    strictPort: true,
    host: host || false,
    watch: {
      ignored: ["**/src-tauri/**"],
    },
  },
  build: {
    outDir: "dist",
    target:
      process.env.TAURI_ENV_PLATFORM === "windows" ? "chrome105" : "safari13",
    minify: !process.env.TAURI_ENV_DEBUG ? "esbuild" : false,
    sourcemap: !!process.env.TAURI_ENV_DEBUG,
  },
});

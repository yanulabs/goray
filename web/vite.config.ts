import { existsSync, renameSync } from "node:fs";

import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { defineConfig, type Plugin } from "vite";
import { viteSingleFile } from "vite-plugin-singlefile";

// Vite emits the HTML entry as index.html; the embedded artifact must be
// report.html. Rename the fully inlined file after the bundle is written.
function renameReportHtml(): Plugin {
  return {
    name: "goray-report-html-name",
    apply: "build",
    closeBundle() {
      const directory = "../internal/render/dist";
      const source = `${directory}/index.html`;
      if (existsSync(source)) {
        renameSync(source, `${directory}/report.html`);
      }
    },
  };
}

// The built report is committed at internal/render/dist/report.html so Go
// installs, builds, and tests never require Node.js.
const apiTarget = process.env.GORAY_DEV_API ?? "http://127.0.0.1:4321";

export default defineConfig({
  base: "./",
  plugins: [
    react(),
    tailwindcss(),
    viteSingleFile({ removeViteModuleLoader: true }),
    renameReportHtml(),
  ],
  server: {
    port: 5173,
    proxy: {
      "/api": {
        target: apiTarget,
        changeOrigin: true,
      },
    },
  },
  build: {
    outDir: "../internal/render/dist",
    emptyOutDir: true,
    target: "es2022",
    cssCodeSplit: false,
    assetsInlineLimit: 100_000_000,
    chunkSizeWarningLimit: 4000,
  },
});

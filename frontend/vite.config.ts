import { execSync } from "node:child_process";
import { defineConfig } from "vite";
import { svelte } from "@sveltejs/vite-plugin-svelte";

function gitCommit(): string {
  try {
    return execSync("git rev-parse --short HEAD", {
      encoding: "utf-8",
    }).trim();
  } catch {
    return "unknown";
  }
}

const apiTarget = process.env.VITE_API_TARGET ?? "http://127.0.0.1:8080";
const apiTargetOrigin = new URL(apiTarget).origin;

export default defineConfig({
  base: "/",
  plugins: [svelte()],
  define: {
    "import.meta.env.VITE_BUILD_COMMIT": JSON.stringify(
      gitCommit(),
    ),
  },
  resolve: {
    conditions: ["browser"],
  },
  server: {
    proxy: {
      "/api": {
        target: apiTarget,
        changeOrigin: true,
        headers: {
          Origin: apiTargetOrigin,
        },
      },
    },
  },
  build: {
    outDir: "dist",
    emptyOutDir: true,
  },
  test: {
    environment: "jsdom",
    exclude: ["e2e/**", "node_modules/**"],
    server: {
      deps: {
        inline: ["svelte"],
      },
    },
  },
});

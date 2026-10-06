/// <reference types="vitest" />
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import { fileURLToPath } from "node:url";

const sharedSrc = fileURLToPath(new URL("../shared/src", import.meta.url));

export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      "@shared": sharedSrc,
    },
  },
  server: {
    port: 5173,
    proxy: {
      "/api": "http://localhost:8765",
      "/ws": { target: "ws://localhost:8765", ws: true },
      "/uploads": "http://localhost:8765",
      "/avatars": "http://localhost:8765",
      "/static": "http://localhost:8765",
      "/fonts": "http://localhost:8765",
      "/sound": "http://localhost:8765",
      "/sw.js": "http://localhost:8765",
    },
  },
  build: {
    outDir: "dist",
    sourcemap: true,
  },
  test: {
    environment: "node",
  },
});
